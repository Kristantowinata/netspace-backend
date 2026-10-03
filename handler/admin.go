package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/latoiste/netspace/api"
	"github.com/latoiste/netspace/auth"
	"github.com/latoiste/netspace/model"
)

// wib is the app's reporting timezone (Asia/Jakarta, UTC+7, no DST). Analytics
// buckets (per-hour, per-day) are computed in WIB so they match what the café
// admin actually sees — independent of the server's timezone (UTC in prod).
// A fixed offset avoids depending on tzdata being present in the image.
var wib = time.FixedZone("WIB", 7*60*60)

func adminSessionForLocation(r *http.Request, locationSlug string) (model.SessionData, bool) {
	session, ok := r.Context().Value("SessionData").(model.SessionData)
	if !ok || session.ActorType != "admin" || session.LocationSlug != locationSlug {
		return model.SessionData{}, false
	}
	return session, true
}

func (h *Handler) handleAnalyticsMetrics() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		analytics := make([]api.AnalyticsDTO, 0, 3)

		now := time.Now().In(wib)

		todayStart := time.Date(
			now.Year(),
			now.Month(),
			now.Day(),
			0, 0, 0, 0,
			now.Location(),
		)
		todayCurrent := now

		yesterdayStart := todayStart.AddDate(0, 0, -1)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		checkInToday, checkInYest, err := h.repo.CheckInAnalytics(yesterdayStart, todayStart, todayCurrent, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var checkInDeltaType string
		if checkInToday >= checkInYest {
			checkInDeltaType = "up"
		} else {
			checkInDeltaType = "down"
		}
		checkInAnalytics := api.ConstructAnalyticsDTO(checkInYest, checkInToday, "Check-in Hari Ini", checkInDeltaType)

		// "User Aktif Sekarang" = live WebSocket connections right now, not the
		// DB isactive flag. The flag drifts: a process killed before its
		// disconnect handlers run leaves ghosts marked active, and the previous
		// query also only counted users created *today*. Counting live sockets
		// reflects who is actually connected this instant.
		activeNow := h.manager.TotalOnline()
		activeUsersAnalytics := api.ConstructAnalyticsDTO(0, activeNow, "User Aktif Sekarang", "live")

		ctx3, cancel3 := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel3()

		messagesToday, messagesYest, err := h.repo.MessagesAnalytics(yesterdayStart, todayStart, todayCurrent, ctx3)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var messagesDeltaType string
		if messagesToday >= messagesYest {
			messagesDeltaType = "up"
		} else {
			messagesDeltaType = "down"
		}
		messagesAnalytics := api.ConstructAnalyticsDTO(messagesYest, messagesToday, "Total Konversasi Hari Ini", messagesDeltaType)

		analytics = append(analytics, checkInAnalytics)
		analytics = append(analytics, activeUsersAnalytics)
		analytics = append(analytics, messagesAnalytics)

		if err := json.NewEncoder(w).Encode(analytics); err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func (h *Handler) handleHourlyCheckIn() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		now := time.Now().In(wib)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		response, err := h.repo.HourlyCheckInAnalytics(8, now, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func (h *Handler) handleAdminLogin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")

		writeLoginError := func(status int, message string) {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(api.AdminLoginResponse{
				Success: false,
				Error:   message,
			})
		}

		var reqBody api.AdminLoginRequest

		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Println(err)
			writeLoginError(http.StatusBadRequest, "Username dan password harus diisi")
			return
		}

		err = json.Unmarshal(body, &reqBody)
		if err != nil {
			log.Println(err)
			writeLoginError(http.StatusBadRequest, "Username dan password harus diisi")
			return
		}

		username := reqBody.Username
		password := reqBody.Password

		if username == "" || password == "" {
			writeLoginError(http.StatusBadRequest, "Username dan password harus diisi")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		admin, err := h.repo.AdminByUsername(username, ctx)
		if err != nil {
			log.Println(err)
			writeLoginError(http.StatusUnauthorized, "Username atau password salah")
			return
		}

		if !auth.CheckPassword(admin.Password, password) {
			writeLoginError(http.StatusUnauthorized, "Username atau password salah")
			return
		}

		// Upgrade legacy plaintext rows to bcrypt on first successful login.
		// Best-effort: a failure here must not block the login itself.
		if !auth.IsPasswordHashed(admin.Password) {
			if hash, err := auth.HashPassword(password); err != nil {
				log.Println("failed to hash admin password:", err)
			} else if err := h.repo.UpdateAdminPassword(admin.Id, hash, ctx); err != nil {
				log.Println("failed to upgrade admin password hash:", err)
			}
		}

		token, err := h.auth.GenerateAdminJWT(admin.Id, admin.LocationSlug)
		if err != nil {
			log.Println(err)
			writeLoginError(http.StatusInternalServerError, "Gagal membuat sesi login")
			return
		}

		adminDto := api.ConstructAdminDTO(*admin)
		response := api.AdminLoginResponse{
			Success: true,
			Admin:   &adminDto,
			Token:   token,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func (h *Handler) handleTopInterests() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		response, err := h.repo.GetTopInterests(ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func (h *Handler) handleLocationDetail() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		locationSlug := r.PathValue("slug")
		if _, ok := adminSessionForLocation(r, locationSlug); !ok {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		location, err := h.repo.LocationBySlug(locationSlug, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		response := api.GetLocationDetailResponse{
			Slug:       location.Slug,
			Name:       location.Name,
			Address:    location.Address,
			PartnerId:  location.PartnerId,
			JoinedDate: location.JoinDate.Format("02 Jan 2006"),
			Capacity:   fmt.Sprintf("~ %v user", location.Capacity),
			Timezone:   location.FormatTimezoneLabel(),
			IsActive:   location.IsActive,
			QrToken:    location.QrToken,
			QrLabel:    location.QrLabel,
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func (h *Handler) handleToggleLocationStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		locationSlug := r.PathValue("slug")
		if _, ok := adminSessionForLocation(r, locationSlug); !ok {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		type Payload struct {
			IsActive bool `json:"isActive"`
		}

		var reqBody Payload

		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err = json.Unmarshal(body, &reqBody)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		err = h.repo.UpdateLocationIsActive(locationSlug, reqBody.IsActive, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte("{\"success\": true}"))
	}
}

func (h *Handler) handleActiveUsers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		locationSlug := r.PathValue("slug")
		if _, ok := adminSessionForLocation(r, locationSlug); !ok {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		ctx1, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		id, err := h.repo.LocationIdBySlug(locationSlug, ctx1)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ctx2, cancel := context.WithTimeout(context.Background(), time.Second*2)
		defer cancel()

		users, err := h.repo.UsersInLocation(id, ctx2)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		response := api.ConstructGetActiveUsersResponse(users)

		if err = json.NewEncoder(w).Encode(response); err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func (h *Handler) handleAdminPublicHistory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		locationSlug := r.PathValue("slug")
		session, ok := adminSessionForLocation(r, locationSlug)
		if !ok {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), time.Second*3)
		defer cancel()
		locationId, err := h.repo.LocationIdBySlug(locationSlug, ctx)
		if err != nil {
			http.Error(w, "Location not found", http.StatusNotFound)
			return
		}
		msgs, err := h.repo.RecentPublicMessages(
			locationId,
			publicHistoryLimit,
			time.Now().Add(-publicHistoryWindow),
			ctx,
		)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for i := range msgs {
			msgs[i].IsMine = msgs[i].IsAdmin && msgs[i].SenderId == session.UserId
		}
		if err := json.NewEncoder(w).Encode(api.GetPublicHistoryResponse{Messages: msgs}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func (h *Handler) handleDeleteAllPublicMessages() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		locationSlug := r.PathValue("slug")
		if _, ok := adminSessionForLocation(r, locationSlug); !ok {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), time.Second*5)
		defer cancel()
		locationId, err := h.repo.LocationIdBySlug(locationSlug, ctx)
		if err != nil {
			http.Error(w, "Location not found", http.StatusNotFound)
			return
		}
		deletedCount, err := h.repo.DeletePublicMessagesByLocation(locationId, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.manager.BroadcastPublicMessagesCleared(locationSlug, deletedCount)
		json.NewEncoder(w).Encode(map[string]any{
			"success":      true,
			"deletedCount": deletedCount,
		})
	}
}

func (h *Handler) handleForceLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		userId := r.PathValue("userId")

		type Payload struct {
			Reason string `json:"reason"`
		}

		var reqBody Payload

		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err = json.Unmarshal(body, &reqBody)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()

		// Admins may only kick users checked in at their own venue.
		session, ok := r.Context().Value("SessionData").(model.SessionData)
		if !ok {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		adminLocationId, err := h.repo.LocationIdBySlug(session.LocationSlug, ctx)
		if err != nil {
			log.Println(err)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		user, err := h.repo.UserById(userId, ctx)
		if err != nil || user.LocationId != adminLocationId {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}

		if err := h.repo.UpdateUserIsActive(userId, false, ctx); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		h.manager.ForceLogoutUser(userId)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{
			"success": true,
		})
	}
}
