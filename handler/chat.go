package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/latoiste/netspace/api"
	"github.com/latoiste/netspace/model"
)

func (h *Handler) handleChatList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		sessionData, ok := r.Context().Value("SessionData").(model.SessionData)
		if !ok {
			log.Println("Invalid session data value")
			http.Error(w, "Invalid session data", http.StatusUnauthorized)
			return
		}

		userId := sessionData.UserId
		if userId == "" {
			log.Println("No UserId in token")
			http.Error(w, "No UserId in token", http.StatusUnauthorized)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		privateMsgs, err := h.repo.AllPrivateMessages(userId, ctx)
		if err != nil {
			log.Println(err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel2()

		groupMsgs, err := h.repo.GroupsForUser(userId, ctx2)
		if err != nil {
			log.Println(err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		response := api.ConstructGetChatListResponse(privateMsgs, groupMsgs)

		if err = json.NewEncoder(w).Encode(response); err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

// handleDMHistory returns the partner's profile + the full DM message history,
// so reopening a private chat shows past messages instead of an empty thread.
func (h *Handler) handleDMHistory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")

		sessionData, ok := r.Context().Value("SessionData").(model.SessionData)
		if !ok {
			http.Error(w, "Invalid session data", http.StatusUnauthorized)
			return
		}

		myId := sessionData.UserId
		partnerId := chi.URLParam(r, "userId")
		if partnerId == "" {
			http.Error(w, "Missing userId", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
		defer cancel()

		partner, err := h.repo.UserById(partnerId, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}

		msgs, err := h.repo.PrivateMessagesBetween(myId, partnerId, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		response := api.ConstructGetDMHistoryResponse(*partner, msgs, myId)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// publicHistoryLimit / publicHistoryWindow bound how much of the shared public
// timeline a newcomer loads: the most recent messages within the recent past,
// so the room has context without dumping an unbounded backlog.
const (
	publicHistoryLimit  = 50
	publicHistoryWindow = 24 * time.Hour
)

// handlePublicHistory returns the recent public-room messages for a location so
// someone opening the room sees what's been said, not an empty thread.
func (h *Handler) handlePublicHistory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")

		sessionData, ok := r.Context().Value("SessionData").(model.SessionData)
		if !ok {
			http.Error(w, "Invalid session data", http.StatusUnauthorized)
			return
		}
		myId := sessionData.UserId

		slug := chi.URLParam(r, "slug")
		if slug == "" {
			http.Error(w, "Missing slug", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
		defer cancel()

		locationId, err := h.repo.LocationIdBySlug(slug, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, "Location not found", http.StatusNotFound)
			return
		}

		since := time.Now().Add(-publicHistoryWindow)
		msgs, err := h.repo.RecentPublicMessages(locationId, publicHistoryLimit, since, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for i := range msgs {
			msgs[i].IsMine = msgs[i].SenderId == myId
		}

		if err := json.NewEncoder(w).Encode(api.GetPublicHistoryResponse{Messages: msgs}); err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// handleGroupHistory returns the group summary, current members and full
// message history. The member roster doubles as the join-time snapshot.
func (h *Handler) handleGroupHistory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")

		sessionData, ok := r.Context().Value("SessionData").(model.SessionData)
		if !ok {
			http.Error(w, "Invalid session data", http.StatusUnauthorized)
			return
		}

		myId := sessionData.UserId
		groupId := chi.URLParam(r, "groupId")
		if groupId == "" {
			http.Error(w, "Missing groupId", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
		defer cancel()

		group, err := h.repo.GroupById(groupId, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, "Group not found", http.StatusNotFound)
			return
		}

		members, err := h.repo.GroupMembers(groupId, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Only members may read a group's history. Answer exactly like a
		// missing group so outsiders can't probe which group ids exist.
		if !slices.ContainsFunc(members, func(m model.User) bool { return m.Id == myId }) {
			http.Error(w, "Group not found", http.StatusNotFound)
			return
		}

		msgs, err := h.repo.GroupMessagesByGroupId(groupId, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Build a sender lookup; resolve any message authors who already left.
		senders := make(map[string]model.User, len(members))
		for _, m := range members {
			senders[m.Id] = m
		}
		for _, msg := range msgs {
			if _, ok := senders[msg.SenderId]; ok {
				continue
			}
			if u, err := h.repo.UserById(msg.SenderId, ctx); err == nil {
				senders[msg.SenderId] = *u
			}
		}

		response := api.ConstructGetGroupHistoryResponse(*group, members, msgs, senders, myId)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
