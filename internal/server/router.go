package server

import (
	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/a-shan/mysql-pitr/internal/server/agent"
	"github.com/a-shan/mysql-pitr/internal/server/auth"
	"github.com/a-shan/mysql-pitr/internal/server/org"
)

// NewRouter creates and configures a chi router with all API routes mounted.
func NewRouter() *chi.Mux {
	// Shared secret for JWT signing. In production this should come from
	// environment or config.
	jwtSecret := []byte("change-me-in-production")

	// Initialise in-memory stores.
	userStore := auth.NewInMemoryUserStore()
	orgStore := org.NewInMemoryOrgStore()
	agentStore := agent.NewInMemoryAgentStore()

	// Initialise handlers.
	authHandler := auth.NewHandler(userStore, jwtSecret)
	orgHandler := org.NewHandler(orgStore, userStore, jwtSecret)
	agentHandler := agent.NewHandler(agentStore, orgStore, jwtSecret)

	r := chi.NewRouter()

	// Global middleware.
	r.Use(chiMiddleware.Logger)
	r.Use(chiMiddleware.Recoverer)
	r.Use(chiMiddleware.RequestID)
	r.Use(chiMiddleware.RealIP)

	// ---- Public routes ----
	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
		r.Post("/refresh", authHandler.Refresh)
	})

	// ---- Protected routes (JWT required) ----
	r.Group(func(r chi.Router) {
		r.Use(authHandler.AuthMiddleware)

		// Organisation endpoints.
		r.Route("/api/orgs", func(r chi.Router) {
			r.Post("/", orgHandler.Create)

			r.Route("/{id}", func(r chi.Router) {
				r.Post("/invite", orgHandler.Invite)
				r.Post("/accept", orgHandler.AcceptInvite)
				r.Get("/members", orgHandler.ListMembers)
			})
		})

		// Agent endpoints.
		r.Route("/api/agents", func(r chi.Router) {
			r.Post("/register", agentHandler.Register)
			r.Get("/", agentHandler.List)

			r.Route("/{id}", func(r chi.Router) {
				r.Post("/approve", agentHandler.Approve)
				r.Get("/", agentHandler.Get)
			})
		})
	})

	return r
}
