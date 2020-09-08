// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package avm

import (
	"context"

	"github.com/ava-labs/gecko/utils/codec"
	"github.com/ava-labs/gecko/utils/crypto"

	// Import MySQL driver
	_ "github.com/go-sql-driver/mysql"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/health"
)

type DB struct {
	networkID uint32
	chainID   string

	codec  codec.Codec
	stream *health.Stream
	db     *dbr.Connection

	ecdsaRecoveryFactory crypto.FactorySECP256K1R
}

// NewDB creates a new DB for the given config
func NewDB(stream *health.Stream, db *dbr.Connection, networkID uint32, chainID string, codec codec.Codec) *DB {
	return &DB{
		db:        db,
		codec:     codec,
		stream:    stream,
		chainID:   chainID,
		networkID: networkID,

		ecdsaRecoveryFactory: crypto.FactorySECP256K1R{},
	}
}

func (db *DB) Close(context.Context) error {
	db.stream.Event("close")
	return db.db.Close()
}

func (db *DB) newSession(name string) *dbr.Session {
	return db.db.NewSession(db.stream.NewJob(name))
}
