package chat

import (
	"log"

	"github.com/latoiste/netspace/api"
	"github.com/latoiste/netspace/model"
)

func (h *Hub) handleSendPrivateMsg(msg model.PrivateMessage) {
	sender, ok := h.Clients[msg.SenderId]
	if !ok {
		log.Println("Sender id not found")
		return
	}

	recipient, ok := h.Clients[msg.RecipientId]
	if !ok {
		log.Println("Recipient id not found")
		return
	}

	newMsg := api.NewMessage{
		MessageId: msg.MessageId,
		SenderId:  msg.SenderId,
		Message:   msg.Message,
		Timestamp: msg.Timestamp.Local().Format("15:04"),
	}

	msgSent := api.MessageSent{
		MessageId: msg.MessageId,
		Timestamp: msg.Timestamp.Local().Format("15:04"),
	}

	senderMsg := newMsg
	senderMsg.IsMine = true

	recipientMsg := newMsg
	recipientMsg.IsMine = false

	h.persistPrivateMsg <- msg

	// Always echo the message back to the sender so their own thread updates,
	// even if the recipient has blocked them (the sender isn't told).
	err := sender.sendEvent("new_message", senderMsg)
	if err != nil {
		log.Println(err)
		return
	}

	err = sender.sendEvent("message_sent", msgSent)
	if err != nil {
		log.Println(err)
		return
	}

	// If the recipient blocked the sender, don't deliver the message or the
	// notification to them.
	if h.isBlocked(msg.RecipientId, msg.SenderId) {
		return
	}

	err = recipient.sendEvent("new_message", recipientMsg)
	if err != nil {
		log.Println(err)
		return
	}

	// Notify the recipient ("X mengirim pesan") so the message surfaces in
	// their notification list even if they aren't on the chat screen.
	notif := model.NewMessageNotif(sender.Emoji, msg.Timestamp, sender.Name, msg.Message, sender.UserId)
	h.sendNotification(recipient, notif)
}

func (h *Hub) handleSendPublicMsg(msg model.PublicMessage) {
	newMsg := api.NewPublicMessage{
		MessageId:   msg.MessageId,
		SenderId:    msg.SenderId,
		SenderName:  msg.SenderName,
		SenderEmoji: msg.SenderEmoji,
		Message:     msg.Message,
		Timestamp:   msg.Timestamp.Local().Format("15:04"),
		IsAdmin:     msg.IsAdmin,
	}
	if msg.IsAdmin {
		newMsg.SenderId = msg.AdminId
	}

	h.persistPublicMsg <- msg

	for _, client := range h.Clients {
		// Skip clients who have blocked the sender.
		if !msg.IsAdmin && h.isBlocked(client.UserId, msg.SenderId) {
			continue
		}
		clientMsg := newMsg
		clientMsg.IsMine = !msg.IsAdmin && client.UserId == msg.SenderId
		err := client.sendEvent("new_public_message", clientMsg)
		if err != nil {
			log.Println(err)
			continue
		}
	}
	for _, moderator := range h.Moderators {
		moderatorMsg := newMsg
		moderatorMsg.IsMine = msg.IsAdmin && moderator.UserId == msg.AdminId
		if err := moderator.sendEvent("new_public_message", moderatorMsg); err != nil {
			log.Println(err)
		}
	}
}

func (h *Hub) handleSendGroupMsg(msg model.GroupMessage) {
	sender, ok := h.Clients[msg.SenderId]
	if !ok {
		log.Println("Sender id not foudn")
		return
	}

	group, ok := h.groups[msg.GroupId]
	if !ok {
		log.Println("Group id not found")
		return
	}

	if !group.memberIds[msg.SenderId] {
		log.Printf("Non-member %v tried to message group %v\n", msg.SenderId, group.name)
		return
	}

	newMsg := api.NewGroupMessage{
		MessageId:   msg.MessageId,
		GroupId:     msg.GroupId,
		SenderId:    msg.SenderId,
		SenderName:  sender.Name,
		SenderEmoji: sender.Emoji,
		Message:     msg.Message,
		Timestamp:   msg.Timestamp.Local().Format("15:04"),
	}

	senderMsg := newMsg
	senderMsg.IsMine = true

	otherMsg := newMsg
	otherMsg.IsMine = false

	h.persistGroupMsg <- msg

	err := sender.sendEvent("new_group_message", senderMsg)
	if err != nil {
		log.Println(err)
		return
	}

	for memberId := range group.memberIds {
		if memberId == sender.UserId {
			continue
		}
		// Skip members who have blocked the sender.
		if h.isBlocked(memberId, sender.UserId) {
			continue
		}
		client, ok := h.Clients[memberId]
		if !ok {
			log.Printf("client %v not found in group %v\n", memberId, group.name)
			continue
		}
		err := client.sendEvent("new_group_message", otherMsg)
		if err != nil {
			log.Println(err)
			continue
		}
	}
}
