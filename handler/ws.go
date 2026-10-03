package handler

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/latoiste/netspace/api"
	"github.com/latoiste/netspace/auth"
	"github.com/latoiste/netspace/chat"
)

func (h *Handler) handleWs(manager *chat.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		params := r.URL.Query()
		tokenString := params.Get("token")

		// A logged-out session must not be able to open a new socket either.
		if h.blacklist.IsBlacklisted(tokenString) {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		token, err := h.auth.VerifyToken(tokenString)
		if err != nil {
			log.Println("Faield to verify token")
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		claim, ok := token.Claims.(*auth.Claim)
		if !ok {
			log.Println("Invalid token fields")
			http.Error(w, "Invalid token fields", http.StatusUnauthorized)
			return
		}

		locationSlug := params.Get("locationSlug")
		actorType := claim.ActorType
		if actorType == "" {
			actorType = "user"
		}
		if actorType == "admin" && claim.LocationSlug != locationSlug {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		locationId, err := h.repo.LocationIdBySlug(locationSlug, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, "Location not found", http.StatusNotFound)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		hub := manager.LocationHub(locationSlug, locationId)

		ctx2, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		if actorType == "admin" {
			admin, err := h.repo.AdminById(claim.UserId, ctx2)
			if err != nil || admin.LocationSlug != locationSlug {
				log.Println(err)
				conn.Close()
				return
			}
			client := chat.NewAdminClient(
				hub,
				conn,
				admin.Id,
				admin.Name,
				admin.Avatar,
				locationSlug,
			)
			hub.AddModerator(client)
			go client.ReadPump()
			go client.WritePump()
			return
		}

		user, err := h.repo.UserById(claim.UserId, ctx2)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			conn.Close()
			return
		}

		client := chat.NewClient(
			hub,
			conn,
			claim.UserId,
			user.Name,
			api.EmojiForUser(claim.UserId),
			locationSlug,
		)

		err = hub.AddClient(client, user)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			conn.Close()
			return
		}

		// A prior disconnect (network blip, tab reload, reconnect) flips the
		// user's isActive flag to false. Nothing else restores it, so without
		// this a reconnected user stays filtered out of the roster fetch
		// (/api/locations/{slug}/users WHERE isActive = true) and silently
		// "disappears" even though their socket is live again.
		ctx3, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()
		if err := h.repo.UpdateUserIsActive(claim.UserId, true, ctx3); err != nil {
			log.Println("failed to mark user active on connect:", err)
		}

		go client.ReadPump()
		go client.WritePump()
	}
}
