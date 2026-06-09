package api

import (
	"barberbase-core/internal/bhejna"
	"barberbase-core/internal/config"
	"barberbase-core/internal/domain/presence"
	"barberbase-core/internal/realtime"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Server implements ServerInterface.
// All handler files in this package define methods on *Server.
// Dependencies are injected at startup and are read-only after init.
type Server struct {
	Unimplemented
	Pool    *pgxpool.Pool
	Bhejna  bhejna.Client
	Config  *config.Config
	Arrival *presence.Service
	Manager *realtime.Manager // nil-safe; SSE disabled if nil
}
