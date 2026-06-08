package repository

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate checks if the tenants table exists in the database.
// If it does not, it reads the migration SQL from the provided path and executes it.
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrationFilePath string) error {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT FROM pg_tables 
			WHERE schemaname = 'public' 
			AND tablename = 'tenants'
		);
	`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if tenants table exists: %w", err)
	}

	if exists {
		log.Println("Database already initialized. Skipping migrations.")
		return nil
	}

	log.Printf("Applying database migration from %s...", migrationFilePath)

	content, err := os.ReadFile(migrationFilePath)
	if err != nil {
		return fmt.Errorf("failed to read migration file %s: %w", migrationFilePath, err)
	}

	// pgx Exec supports executing multi-statement SQL strings
	_, err = pool.Exec(ctx, string(content))
	if err != nil {
		return fmt.Errorf("failed to execute migration script: %w", err)
	}

	log.Println("Database migration completed successfully!")
	return nil
}
