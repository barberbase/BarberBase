package api

import (
	"barberbase-core/internal/bhejna"
	"barberbase-core/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Server implements ServerInterface.
// All handler files in this package define methods on *Server.
// Dependencies are injected at startup and are read-only after init.
type Server struct {
	Unimplemented
	Pool   *pgxpool.Pool
	Bhejna bhejna.Client
	Config *config.Config
}
