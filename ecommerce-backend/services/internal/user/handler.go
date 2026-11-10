// Package user implements registration, login, profile and notification endpoints.
package user

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"ecommerce-backend/services/internal/common"
)

// Service holds the dependencies of the user service.
type Service struct {
	DB     *sql.DB
	Secret []byte
}

// Profile is the public view of a user. The password hash is never included.
type Profile struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// Notification is a message recorded by the Python notification worker.
type Notification struct {
	ID        int       `json:"id"`
	OrderID   int       `json:"order_id"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type authResponse struct {
	Token string  `json:"token"`
	User  Profile `json:"user"`
}

type credentials struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Routes returns the HTTP handler for this service.
func (s *Service) Routes() http.Handler {
	auth := func(h http.HandlerFunc) http.Handler { return common.Require(s.Secret, h) }

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.Handle("GET /api/users/me", auth(s.me))
	mux.Handle("GET /api/notifications", auth(s.notifications))
	mux.Handle("GET /health", common.Health("user"))
	return mux
}

func (s *Service) register(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if err := common.DecodeJSON(w, r, &in); err != nil {
		common.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(in.Name)
	email := strings.ToLower(strings.TrimSpace(in.Email))

	switch {
	case name == "" || len(name) > 100:
		common.WriteError(w, http.StatusBadRequest, "name is required (max 100 characters)")
		return
	case len(email) > 255 || !validEmail(email):
		common.WriteError(w, http.StatusBadRequest, "a valid email address is required")
		return
	case len(in.Password) < 8 || len(in.Password) > 128:
		common.WriteError(w, http.StatusBadRequest, "password must be 8 to 128 characters")
		return
	}

	hash, err := common.HashPassword(in.Password)
	if err != nil {
		common.ServerError(w, "hash password", err)
		return
	}

	var p Profile
	err = s.DB.QueryRowContext(r.Context(),
		`INSERT INTO users (name, email, password_hash) VALUES ($1, $2, $3)
		 RETURNING id, name, email, created_at`,
		name, email, hash,
	).Scan(&p.ID, &p.Name, &p.Email, &p.CreatedAt)
	if common.IsUniqueViolation(err) {
		common.WriteError(w, http.StatusConflict, "an account with this email already exists")
		return
	}
	if err != nil {
		common.ServerError(w, "insert user", err)
		return
	}
	s.respondWithToken(w, http.StatusCreated, p)
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if err := common.DecodeJSON(w, r, &in); err != nil {
		common.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))

	var p Profile
	var hash string
	err := s.DB.QueryRowContext(r.Context(),
		`SELECT id, name, email, created_at, password_hash FROM users WHERE email = $1`, email,
	).Scan(&p.ID, &p.Name, &p.Email, &p.CreatedAt, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		common.WriteError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		common.ServerError(w, "load user", err)
		return
	}
	if !common.CheckPassword(in.Password, hash) {
		common.WriteError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	s.respondWithToken(w, http.StatusOK, p)
}

func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.CurrentUser(r)
	var p Profile
	err := s.DB.QueryRowContext(r.Context(),
		`SELECT id, name, email, created_at FROM users WHERE id = $1`, claims.Sub,
	).Scan(&p.ID, &p.Name, &p.Email, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		common.WriteError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		common.ServerError(w, "load profile", err)
		return
	}
	common.WriteJSON(w, http.StatusOK, p)
}

func (s *Service) notifications(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.CurrentUser(r)
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT id, order_id, message, created_at FROM notifications
		  WHERE user_id = $1 ORDER BY id DESC LIMIT 50`, claims.Sub)
	if err != nil {
		common.ServerError(w, "list notifications", err)
		return
	}
	defer rows.Close()

	items := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.OrderID, &n.Message, &n.CreatedAt); err != nil {
			common.ServerError(w, "scan notification", err)
			return
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		common.ServerError(w, "iterate notifications", err)
		return
	}
	common.WriteJSON(w, http.StatusOK, items)
}

func (s *Service) respondWithToken(w http.ResponseWriter, status int, p Profile) {
	token, err := common.IssueToken(s.Secret, p.ID, p.Email, common.TokenTTL)
	if err != nil {
		log.Printf("issue token: %v", err)
		common.WriteError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	common.WriteJSON(w, status, authResponse{Token: token, User: p})
}

func validEmail(email string) bool {
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}
