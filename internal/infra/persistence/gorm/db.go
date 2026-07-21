package gorm

import (
	"fmt"

	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/gorm"
)

// Open ouvre la base SQLite à path et exécute les migrations automatiques.
// Utilise github.com/ncruces/go-sqlite3 (SQLite compilé en WASM, sans CGO)
// plutôt que mattn/go-sqlite3, pour des builds simples et portables.
//
// La base est ouverte en mode WAL (Write-Ahead Logging) : contrairement au
// journal rollback par défaut, WAL autorise plusieurs lecteurs concurrents en
// parallèle d'un unique rédacteur, sans erreur "database is locked". C'est ce
// qui permet à edecán de servir plusieurs utilisateurs simultanément sans
// sérialiser toutes les requêtes DB sur une seule connexion. Le busy_timeout
// fait patienter une écriture le temps qu'une autre se termine, au lieu
// d'échouer immédiatement. L'ordre des pragmas importe pour ce driver
// (busy_timeout avant journal_mode).
func Open(path string) (*gorm.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)
	db, err := gorm.Open(gormlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("ouverture sqlite %q: %w", path, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("récupération du pool sqlite: %w", err)
	}
	// WAL autorise les lectures concurrentes : on ouvre plusieurs connexions
	// pour que les requêtes de lecture ne se bloquent plus mutuellement. Les
	// écritures restent sérialisées par SQLite (un seul rédacteur), mais
	// attendent via le busy_timeout ci-dessus plutôt que d'échouer.
	sqlDB.SetMaxOpenConns(16)
	sqlDB.SetMaxIdleConns(16)

	if err := db.AutoMigrate(
		&UserRow{},
		&SessionRow{},
		&MessageRow{},
		&TicketMappingRow{},
		&RelevanceFlagRow{},
		&SharedConversationRow{},
	); err != nil {
		return nil, fmt.Errorf("migration automatique: %w", err)
	}

	return db, nil
}
