package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// User represents a registered platform user.
type User struct {
	ID             string    `json:"id"`
	Email          string    `json:"email"`
	HashedPassword string    `json:"-"`
	CreatedAt      time.Time `json:"createdAt"`
}

// UserStore defines the interface for user persistence.
type UserStore interface {
	Create(user *User) error
	GetByID(id string) (*User, error)
	GetByEmail(email string) (*User, error)
}

// InMemoryUserStore is a thread-safe in-memory implementation of UserStore.
type InMemoryUserStore struct {
	mu      sync.RWMutex
	byID    map[string]*User
	byEmail map[string]*User
}

// NewInMemoryUserStore creates an initialised InMemoryUserStore.
func NewInMemoryUserStore() *InMemoryUserStore {
	return &InMemoryUserStore{
		byID:    make(map[string]*User),
		byEmail: make(map[string]*User),
	}
}

// Create stores a new user. Email must be unique.
func (s *InMemoryUserStore) Create(user *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byEmail[user.Email]; exists {
		return fmt.Errorf("user with email %s already exists", user.Email)
	}
	if user.ID == "" {
		user.ID = generateID()
	}
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now()
	}
	s.byID[user.ID] = user
	s.byEmail[user.Email] = user
	return nil
}

// GetByID retrieves a user by its ID.
func (s *InMemoryUserStore) GetByID(id string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("user not found: %s", id)
	}
	return user, nil
}

// GetByEmail retrieves a user by its email address.
func (s *InMemoryUserStore) GetByEmail(email string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.byEmail[email]
	if !ok {
		return nil, fmt.Errorf("user not found: %s", email)
	}
	return user, nil
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
