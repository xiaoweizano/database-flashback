package auth

import (
	"encoding/json"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Handler serves authentication HTTP endpoints.
type Handler struct {
	userStore UserStore
	jwtSecret []byte
}

// NewHandler creates an auth Handler.
func NewHandler(userStore UserStore, jwtSecret []byte) *Handler {
	return &Handler{
		userStore: userStore,
		jwtSecret: jwtSecret,
	}
}

// ---------- request / response types ----------

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registerResponse struct {
	Token string `json:"token"`
	User  *User  `json:"user"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	User         *User  `json:"user"`
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type refreshResponse struct {
	Token string `json:"token"`
}

// ---------- handlers ----------

// Register creates a new user account and returns a JWT access token.
//
// POST /api/auth/register
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword(
		[]byte(req.Password), BcryptCost,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	user := &User{
		Email:          req.Email,
		HashedPassword: string(hashedPassword),
	}
	if err := h.userStore.Create(user); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	token, err := CreateToken(user.ID, user.Email, h.jwtSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	writeJSON(w, http.StatusCreated, registerResponse{
		Token: token,
		User:  user,
	})
}

// Login authenticates a user and returns a JWT access token together with a
// refresh token.
//
// POST /api/auth/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, err := h.userStore.GetByEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	if err := bcrypt.CompareHashAndPassword(
		[]byte(user.HashedPassword), []byte(req.Password),
	); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	token, err := CreateToken(user.ID, user.Email, h.jwtSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	refreshToken, err := CreateRefreshToken(user.ID, h.jwtSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create refresh token")
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User:         user,
	})
}

// Refresh accepts a valid refresh token and returns a new JWT access token.
//
// POST /api/auth/refresh
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refreshToken is required")
		return
	}

	userID, err := ValidateRefreshToken(req.RefreshToken, h.jwtSecret)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}

	user, err := h.userStore.GetByID(userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}

	token, err := CreateToken(user.ID, user.Email, h.jwtSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	writeJSON(w, http.StatusOK, refreshResponse{Token: token})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
