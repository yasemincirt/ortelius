package services

import (
	"context"
	"time"

	"github.com/ava-labs/gecko/ids"
	"github.com/gocraft/dbr"
	"github.com/gocraft/health"
)

type Indexable interface {
	ID() ids.ID
	ChainID() ids.ID
	Body() []byte
	Timestamp() uint64
}

// Indexer takes in Indexables and adds them to the services backend
type Indexer interface {
	Index(Indexable) error
}

// FanOutIndexer takes in items and sends them to multiple backend Indexers
type FanOutIndexer []Indexer

// Add takes in an Indexable and sends it to all underlying backends
func (fos FanOutIndexer) Index(i Indexable) (err error) {
	for _, service := range fos {
		if err = service.Index(i); err != nil {
			return err
		}
	}
	return nil
}

// IndexerCtx
type IndexerCtx struct {
	ctx context.Context
	job *health.Job
	db  dbr.SessionRunner

	indexable Indexable
}

func NewIndexerContext(job *health.Job, db dbr.SessionRunner, i Indexable) IndexerCtx {
	return IndexerCtx{
		job:       job,
		db:        db,
		indexable: i,
	}
}

func (ic IndexerCtx) Context() context.Context { return ic.ctx }
func (ic IndexerCtx) Job() *health.Job         { return ic.job }
func (ic IndexerCtx) DB() dbr.SessionRunner    { return ic.db }
func (ic IndexerCtx) ID() ids.ID               { return ic.indexable.ID() }
func (ic IndexerCtx) ChainID() ids.ID          { return ic.indexable.ChainID() }
func (ic IndexerCtx) Body() []byte             { return ic.indexable.Body() }
func (ic IndexerCtx) Time() time.Time          { return time.Unix(int64(ic.indexable.Timestamp()), 0) }
