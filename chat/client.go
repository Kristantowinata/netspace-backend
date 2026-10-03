package chat

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/latoiste/netspace/api"
	"github.com/latoiste/netspace/model"
)

// idleSafetyTimeout bounds how long the server keeps a silent WebSocket open.
// It's deliberately a few minutes longer than the frontend's 30-minute idle
// logout so that a clean, client-driven logout wins in the normal case. This
// only reaps "ghost" connections whose tab or network vanished without sending
// a close frame (the frontend sends a throttled "ping" on activity to keep an
// active-but-quiet reader's connection alive).
const idleSafetyTimeout = 35 * time.Minute

// maxInboundFrameBytes caps a single inbound WebSocket frame. Chat events are
// small JSON objects; anything larger is rejected and the connection closed,
// so one client can't make the server buffer arbitrarily large payloads.
const maxInboundFrameBytes = 64 * 1024

type Client struct {
	Hub          *Hub
	conn         *websocket.Conn
	Send         chan []byte
	UserId       string
	Name         string
	Emoji        string
	LocationSlug string
	IsAdmin      bool

	// closeOnce guards Send so it's closed exactly once. A client can be torn
	// down from more than one spot in the hub's run() goroutine (e.g. evicted as
	// a backlogged client during a broadcast, then again when its ReadPump fires
	// unregister), and closing a channel twice panics.
	closeOnce sync.Once
}

// closeSend closes the client's Send channel exactly once, signalling its
// WritePump to finish. Safe to call repeatedly.
func (c *Client) closeSend() {
	c.closeOnce.Do(func() {
		close(c.Send)
	})
}

func NewClient(hub *Hub, conn *websocket.Conn, userId string, name string, emoji string, locationSlug string) *Client {
	return &Client{
		Hub:          hub,
		conn:         conn,
		Send:         make(chan []byte, 512),
		UserId:       userId,
		Name:         name,
		Emoji:        emoji,
		LocationSlug: locationSlug,
	}
}

func NewAdminClient(hub *Hub, conn *websocket.Conn, adminId string, name string, emoji string, locationSlug string) *Client {
	client := NewClient(hub, conn, adminId, name, emoji, locationSlug)
	client.IsAdmin = true
	return client
}

func (c *Client) ReadPump() {
	defer func() {
		log.Println("Connection is closed")
		// Hand off to the hub. removeClient() decides whether this socket is the
		// user's current live one; only then does it mark them offline in the DB
		// (via handleClientDisconnect). Doing the flip here unconditionally would
		// mark a user offline even when a newer socket has already replaced this
		// one — e.g. on reconnect — so it must not happen on the stale path.
		c.Hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxInboundFrameBytes)

	for {
		// Refresh the read deadline on every loop. Any inbound frame (including
		// the client's keep-alive "ping") pushes it forward; if nothing arrives
		// within idleSafetyTimeout, ReadJSON returns an i/o timeout and the
		// deferred cleanup below logs the user out and marks them offline.
		c.conn.SetReadDeadline(time.Now().Add(idleSafetyTimeout))

		var req api.WsEvent
		err := c.conn.ReadJSON(&req)
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Println("Connection is closed normally")
				break
			}
			log.Println(err)
			break
		}

		switch req.Event {
		case "ping":
			continue
		case "send_message":
			if c.IsAdmin {
				continue
			}
			var data api.SendMessage
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println("On send_message", err)
				continue
			}

			c.Hub.sendPrivateMsg <- model.PrivateMessage{
				BaseMessage: model.BaseMessage{
					MessageId:  model.GenerateMessageId(),
					LocationId: c.Hub.locationId,
					SenderId:   c.UserId,
					Message:    data.Message,
					Timestamp:  time.Now().UTC(),
				},
				RecipientId: data.RecipientId,
			}
		case "typing_start":
			if c.IsAdmin {
				continue
			}
			msg := c.handleTypingRequest(req)
			if msg != (api.TypingEvent{}) {
				c.Hub.typingStart <- msg
			}
		case "typing_stop":
			if c.IsAdmin {
				continue
			}
			msg := c.handleTypingRequest(req)
			if msg != (api.TypingEvent{}) {
				c.Hub.typingStop <- msg
			}
		case "send_public_message":
			var data api.SendPublicMessage
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println("On send_public_message", err)
				continue
			}

			message := strings.TrimSpace(data.Message)
			if message == "" || len([]rune(message)) > 500 {
				continue
			}
			publicMessage := model.PublicMessage{
				BaseMessage: model.BaseMessage{
					MessageId:  model.GenerateMessageId(),
					LocationId: c.Hub.locationId,
					Message:    message,
					Timestamp:  time.Now().UTC(),
				},
				SenderName:  c.Name,
				SenderEmoji: c.Emoji,
				IsAdmin:     c.IsAdmin,
			}
			if c.IsAdmin {
				publicMessage.AdminId = c.UserId
			} else {
				publicMessage.SenderId = c.UserId
			}
			c.Hub.sendPublicMsg <- publicMessage
		case "public_typing_start":
			if c.IsAdmin {
				continue
			}
			msg := c.handlePublicTypingRequest(req)
			if msg != (api.PublicUserTyping{}) {
				c.Hub.publicTypingStart <- msg
			}
		case "public_typing_stop":
			if c.IsAdmin {
				continue
			}
			msg := c.handlePublicTypingRequest(req)
			if msg != (api.PublicUserTyping{}) {
				c.Hub.publicTypingStop <- msg
			}
		case "create_group":
			if c.IsAdmin {
				continue
			}
			var data api.CreateGroup
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println("On create_group", err)
				continue
			}
			// Route through run() so the hub's group/Client maps are only ever
			// mutated there. The reply carries the created group back to us.
			reply := make(chan createGroupResult, 1)
			c.Hub.createGroupReq <- createGroupRequest{
				name:      data.Name,
				hostId:    c.UserId,
				memberIds: data.MemberIds,
				reply:     reply,
			}
			result := <-reply
			if result.err != nil {
				log.Println("On create_group", result.err)
				continue
			}
			groupCreated := api.GroupCreated{
				GroupId: result.group.id,
				Name:    result.group.name,
			}
			c.sendEvent("group_created", groupCreated)
		case "invite_to_group":
			if c.IsAdmin {
				continue
			}
			var data api.InviteToGroup
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println(err)
				continue
			}

			c.Hub.inviteToGroup <- inviteRequest{
				groupId:   data.GroupId,
				inviterId: c.UserId,
				userIds:   data.UserIds,
			}
		case "rename_group":
			if c.IsAdmin {
				continue
			}
			var data api.RenameGroup
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println("On rename_group", err)
				continue
			}

			c.Hub.renameGroup <- renameRequest{
				groupId: data.GroupId,
				userId:  c.UserId,
				name:    data.Name,
			}
		case "accept_group_invite":
			if c.IsAdmin {
				continue
			}
			var data api.GroupInviteResponse
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println(err)
				continue
			}

			c.Hub.acceptInvite <- GroupInvite{
				groupId: data.GroupId,
				userId:  c.UserId,
			}
		case "reject_group_invite":
			if c.IsAdmin {
				continue
			}
			var data api.GroupInviteResponse
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println("On reject_group_invite", err)
				continue
			}

			c.Hub.rejectInvite <- GroupInvite{
				groupId: data.GroupId,
				userId:  c.UserId,
			}
		case "block_user":
			if c.IsAdmin {
				continue
			}
			var data api.BlockUser
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println("On block_user", err)
				continue
			}

			c.Hub.blockUser <- blockRequest{
				blockerId: c.UserId,
				blockedId: data.UserId,
			}
		case "unblock_user":
			if c.IsAdmin {
				continue
			}
			var data api.UnblockUser
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println("On unblock_user", err)
				continue
			}

			c.Hub.unblockUser <- blockRequest{
				blockerId: c.UserId,
				blockedId: data.UserId,
			}
		case "mark_read":
			if c.IsAdmin {
				continue
			}
			var data api.MarkRead
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println("On mark_read", err)
				continue
			}

			c.Hub.markRead <- markReadRequest{
				readerId: c.UserId,
				senderId: data.SenderId,
			}
		case "leave_group":
			if c.IsAdmin {
				continue
			}
			var data api.LeaveGroup
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println(err)
				continue
			}

			c.Hub.leaveGroup <- GroupInvite{
				groupId: data.GroupId,
				userId:  c.UserId,
			}
		case "send_group_message":
			if c.IsAdmin {
				continue
			}
			var data api.SendGroupMessage
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println("On send_group_message", err)
				continue
			}

			c.Hub.sendGroupMsg <- model.GroupMessage{
				BaseMessage: model.BaseMessage{
					MessageId:  model.GenerateMessageId(),
					LocationId: c.Hub.locationId,
					SenderId:   c.UserId,
					Message:    data.Message,
					Timestamp:  time.Now().UTC(),
				},
				GroupId: data.GroupId,
			}
		case "notification_read":
			if c.IsAdmin {
				continue
			}
			var data api.NotificationRead
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println("On notification_read", err)
				continue
			}

			c.Hub.notificationRead <- data
		case "dismiss_notification":
			if c.IsAdmin {
				continue
			}
			var data api.NotificationDismiss
			err := json.Unmarshal(req.Data, &data)
			if err != nil {
				log.Println("On dismiss_notification", err)
				continue
			}

			c.Hub.notificationDismiss <- notificationDismissReq{
				notificationId: data.NotificationId,
				userId:         c.UserId,
			}
		}
	}
}

func (c *Client) WritePump() {
	defer func() {
		c.conn.Close()
	}()

	for {
		msg, ok := <-c.Send
		if !ok {
			log.Println("Connection is closed by Hub")
			c.conn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		}

		err := c.conn.WriteMessage(websocket.TextMessage, msg)
		if err != nil {
			log.Println(err)
			return
		}
		log.Println("written to client")
	}
}

func (c *Client) sendEvent(event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}

	wsEvent := api.WsEvent{
		Event: event,
		Data:  payload,
	}

	message, err := json.Marshal(wsEvent)
	if err != nil {
		return err
	}

	c.Send <- message

	return nil
}

func (c *Client) handleTypingRequest(req api.WsEvent) api.TypingEvent {
	var data api.TypingRequest
	err := json.Unmarshal(req.Data, &data)
	if err != nil {
		log.Println(err)
		return api.TypingEvent{}
	}

	return api.TypingEvent{
		SenderId:    c.UserId,
		RecipientId: data.RecipientId,
	}
}

func (c *Client) handlePublicTypingRequest(req api.WsEvent) api.PublicUserTyping {
	var data api.PublicTypingRequest

	err := json.Unmarshal(req.Data, &data)
	if err != nil {
		log.Println(err)
		return api.PublicUserTyping{}
	}

	return api.PublicUserTyping{
		UserId: c.UserId,
		Name:   c.Name,
		Emoji:  c.Emoji,
	}
}

func (c *Client) handleClientDisconnect() {
	if c.IsAdmin {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()

	err := c.Hub.repo.UpdateUserIsActive(c.UserId, false, ctx)
	if err != nil {
		log.Println(err)
	}
}

// forceLogout is the admin "kick" path. Runs on the hub's run() goroutine.
//
// Previously this just closed the socket — but the frontend treats an
// unexpected close as a network blip and auto-reconnects ~1.5s later with the
// same (still-valid) token, re-registering the user and flipping isActive back
// to true. The kick "didn't work": the user reappeared on the next refresh.
//
// Instead, send a force_logout event. The frontend tears the session down
// (blacklists its token, stops reconnecting, returns to the entry screen), so
// the socket closes for good and the user does not come back. If we somehow
// can't enqueue the event, fall back to a hard close.
func (c *Client) forceLogout() {
	if err := c.sendEvent("force_logout", api.ForceLogout{
		Reason: "Kamu dikeluarkan dari sesi oleh admin.",
	}); err != nil {
		log.Println("force logout send:", err)
		c.conn.Close()
	}
}
