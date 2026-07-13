package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAuthTest(t *testing.T) (*Handler, *InMemoryUserStore) {
	t.Helper()
	store := NewInMemoryUserStore()
	handler := NewHandler(store, testSecret)
	return handler, store
}

func authRequest(method, target string, body interface{}) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	return httptest.NewRequest(method, target, &buf)
}

// ---------- Register ----------

func TestRegister_Success(t *testing.T) {
	h, _ := setupAuthTest(t)

	req := authRequest(http.MethodPost, "/api/auth/register", registerRequest{
		Email: "alice@example.com", Password: "securepass",
	})
	w := httptest.NewRecorder()
	h.Register(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp registerResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Token)
	assert.Equal(t, "alice@example.com", resp.User.Email)
	assert.NotEmpty(t, resp.User.ID)
}

func TestRegister_DuplicateEmail(t *testing.T) {
	h, _ := setupAuthTest(t)

	body := registerRequest{Email: "alice@example.com", Password: "pass123"}
	req1 := authRequest(http.MethodPost, "/api/auth/register", body)
	w1 := httptest.NewRecorder()
	h.Register(w1, req1)
	assert.Equal(t, http.StatusCreated, w1.Code)

	req2 := authRequest(http.MethodPost, "/api/auth/register", body)
	w2 := httptest.NewRecorder()
	h.Register(w2, req2)
	assert.Equal(t, http.StatusConflict, w2.Code)
}

func TestRegister_MissingFields(t *testing.T) {
	h, _ := setupAuthTest(t)

	tests := []struct {
		name string
		body interface{}
	}{
		{"empty email", registerRequest{Email: "", Password: "pass"}},
		{"empty password", registerRequest{Email: "a@b.com", Password: ""}},
		{"both empty", registerRequest{Email: "", Password: ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := authRequest(http.MethodPost, "/api/auth/register", tt.body)
			w := httptest.NewRecorder()
			h.Register(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestRegister_InvalidJSON(t *testing.T) {
	h, _ := setupAuthTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/register",
		bytes.NewReader([]byte("{invalid")))
	w := httptest.NewRecorder()
	h.Register(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegister_EmailNormalised(t *testing.T) {
	h, _ := setupAuthTest(t)

	req := authRequest(http.MethodPost, "/api/auth/register", registerRequest{
		Email: "  Alice@Example.COM  ", Password: "pass",
	})
	w := httptest.NewRecorder()
	h.Register(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp registerResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "alice@example.com", resp.User.Email)
}

// ---------- Login ----------

func TestLogin_Success(t *testing.T) {
	h, _ := setupAuthTest(t)

	// Register via handler.
	regReq := authRequest(http.MethodPost, "/api/auth/register", registerRequest{
		Email: "bob@example.com", Password: "pass123",
	})
	w := httptest.NewRecorder()
	h.Register(w, regReq)
	require.Equal(t, http.StatusCreated, w.Code)

	// Login.
	loginReq := authRequest(http.MethodPost, "/api/auth/login", loginRequest{
		Email: "bob@example.com", Password: "pass123",
	})
	w = httptest.NewRecorder()
	h.Login(w, loginReq)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp loginResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Token)
	assert.NotEmpty(t, resp.RefreshToken)
	assert.Equal(t, "bob@example.com", resp.User.Email)
}

func TestLogin_WrongPassword(t *testing.T) {
	h, _ := setupAuthTest(t)

	// Register.
	regReq := authRequest(http.MethodPost, "/api/auth/register", registerRequest{
		Email: "bob@example.com", Password: "pass123",
	})
	w := httptest.NewRecorder()
	h.Register(w, regReq)
	require.Equal(t, http.StatusCreated, w.Code)

	// Login with wrong password.
	loginReq := authRequest(http.MethodPost, "/api/auth/login", loginRequest{
		Email: "bob@example.com", Password: "wrongpass",
	})
	w = httptest.NewRecorder()
	h.Login(w, loginReq)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestLogin_NonexistentUser(t *testing.T) {
	h, _ := setupAuthTest(t)

	loginReq := authRequest(http.MethodPost, "/api/auth/login", loginRequest{
		Email: "nobody@example.com", Password: "pass",
	})
	w := httptest.NewRecorder()
	h.Login(w, loginReq)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ---------- Refresh ----------

func TestRefresh_Success(t *testing.T) {
	h, _ := setupAuthTest(t)

	// Register and login to get refresh token.
	regReq := authRequest(http.MethodPost, "/api/auth/register", registerRequest{
		Email: "carol@example.com", Password: "pass123",
	})
	w := httptest.NewRecorder()
	h.Register(w, regReq)
	require.Equal(t, http.StatusCreated, w.Code)

	loginReq := authRequest(http.MethodPost, "/api/auth/login", loginRequest{
		Email: "carol@example.com", Password: "pass123",
	})
	w = httptest.NewRecorder()
	h.Login(w, loginReq)
	require.Equal(t, http.StatusOK, w.Code)
	var loginResp loginResponse
	_ = json.NewDecoder(w.Body).Decode(&loginResp)

	// Refresh.
	refreshReq := authRequest(http.MethodPost, "/api/auth/refresh", refreshRequest{
		RefreshToken: loginResp.RefreshToken,
	})
	w = httptest.NewRecorder()
	h.Refresh(w, refreshReq)
	assert.Equal(t, http.StatusOK, w.Code)
	var refreshResp refreshResponse
	err := json.NewDecoder(w.Body).Decode(&refreshResp)
	require.NoError(t, err)
	assert.NotEmpty(t, refreshResp.Token)
}

func TestRefresh_InvalidToken(t *testing.T) {
	h, _ := setupAuthTest(t)

	refreshReq := authRequest(http.MethodPost, "/api/auth/refresh", refreshRequest{
		RefreshToken: "not-a-valid-token",
	})
	w := httptest.NewRecorder()
	h.Refresh(w, refreshReq)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRefresh_MissingToken(t *testing.T) {
	h, _ := setupAuthTest(t)

	refreshReq := authRequest(http.MethodPost, "/api/auth/refresh", refreshRequest{
		RefreshToken: "",
	})
	w := httptest.NewRecorder()
	h.Refresh(w, refreshReq)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ---------- AuthMiddleware ----------

func TestAuthMiddleware_ValidToken(t *testing.T) {
	h, _ := setupAuthTest(t)

	token, err := CreateToken("user-1", "a@b.com", testSecret)
	require.NoError(t, err)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		require.NotNil(t, claims)
		assert.Equal(t, "user-1", claims.UserID)
		assert.Equal(t, "a@b.com", claims.Email)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.AuthMiddleware(next).ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	h, _ := setupAuthTest(t)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	h.AuthMiddleware(next).ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_BadScheme(t *testing.T) {
	h, _ := setupAuthTest(t)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()
	h.AuthMiddleware(next).ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	h, _ := setupAuthTest(t)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	w := httptest.NewRecorder()
	h.AuthMiddleware(next).ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
