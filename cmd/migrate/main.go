// Command migrate applies the repository's SQL migrations to a PostgreSQL
// database.
//
// It is the single way the schema is brought up to date: the compose stack
// runs it as a one-shot service before the gateway starts, and a local
// deployment runs it directly. Both paths use the same code, so there is no
// second mechanism that can produce a different schema.
//
// Usage:
//
//	migrate -database-url "$DATABASE_URL" -migrations ./migrations
//
// The flags fall back to the environment: DATABASE_URL for the connection
// string, and the ./migrations directory for the files. A failed migration
// exits non-zero, which is what makes the compose gate "gateway only starts
// after a successful migration" enforceable.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/migrate"
)

func main() {
	log.SetFlags(0)

	var (
		databaseURL = flag.String("database-url", "", "PostgreSQL connection string (default: $DATABASE_URL)")
		migrations  = flag.String("migrations", "migrations", "directory containing the NNNN_*.sql files")
	)
	flag.Parse()

	url := strings.TrimSpace(*databaseURL)
	if url == "" {
		url = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if url == "" {
		log.Fatal("migrate: -database-url or DATABASE_URL is required")
	}

	result, err := migrate.Apply(context.Background(), url, *migrations)
	if err != nil {
		// The error text carries the failing file and the server's reason;
		// no connection string or credential is included in it.
		log.Fatalf("migrate: %v", err)
	}

	switch {
	case result.Bootstrapped:
		fmt.Printf("migrate: bootstrapped bookkeeping; %d migration(s) verified up to date\n", result.Recorded)
	case len(result.Applied) == 0:
		fmt.Printf("migrate: database is up to date (%d migration(s) recorded)\n", result.Recorded)
	default:
		fmt.Printf("migrate: applied %d migration(s): %s\n", len(result.Applied), strings.Join(result.Applied, ", "))
	}
}
