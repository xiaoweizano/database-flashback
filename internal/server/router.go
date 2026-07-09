package server

import (
	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/a-shan/mysql-pitr/internal/server/agent"
	"github.com/a-shan/mysql-pitr/internal/server/audit"
	"github.com/a-shan/mysql-pitr/internal/server/auth"
	"github.com/a-shan/mysql-pitr/internal/server/org"
	"github.com/a-shan/mysql-pitr/internal/server/pitr"
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
	pitrStore := pitr.NewInMemoryOperationStore()
	auditStore := audit.NewInMemoryAuditStore()

	// Initialise handlers.
	authHandler := auth.NewHandler(userStore, jwtSecret)
	orgHandler := org.NewHandler(orgStore, userStore, jwtSecret)
	agentHandler := agent.NewHandler(agentStore, orgStore, jwtSecret)
	pitrHandler := pitr.NewHandler(pitrStore, agentStore, orgStore, auditStore, jwtSecret)
	auditHandler := audit.NewHandler(auditStore, orgStore, jwtSecret)

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

		// PITR workflow endpoints.
		r.Route("/api/pitr", func(r chi.Router) {
			r.Post("/start", pitrHandler.Start)

			r.Route("/{id}", func(r chi.Router) {
				r.Get("/status", pitrHandler.Status)
				r.Post("/cancel", pitrHandler.Cancel)
				r.Get("/preview", pitrHandler.Preview)
				r.Get("/progress", pitrHandler.Progress)
			})
		})

		// Audit log endpoints.
		r.Route("/api/audit", func(r chi.Router) {
			r.Get("/", auditHandler.Query)
			r.Get("/export", auditHandler.Export)
		})
	})

	return r
}
