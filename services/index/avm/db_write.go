// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package avm

import (
	"context"
	"errors"

	"github.com/ava-labs/gecko/ids"
	"github.com/ava-labs/gecko/utils/codec"
	"github.com/ava-labs/gecko/utils/hashing"
	"github.com/ava-labs/gecko/utils/math"
	"github.com/ava-labs/gecko/utils/wrappers"
	"github.com/ava-labs/gecko/vms/avm"
	"github.com/ava-labs/gecko/vms/secp256k1fx"
	"github.com/gocraft/dbr"
	"github.com/gocraft/health"

	"github.com/ava-labs/ortelius/services"
	"github.com/ava-labs/ortelius/services/index"
)

func (db *DB) bootstrap(ctx context.Context, genesisBytes []byte, timestamp int64) error {
	var (
		err  error
		job  = db.stream.NewJob("bootstrap")
		sess = db.db.NewSession(job)
	)
	job.KeyValue("chain_id", db.chainID)

	defer func() {
		if err != nil {
			job.CompleteKv(health.Error, health.Kvs{"err": err.Error()})
			return
		}
		job.Complete(health.Success)
	}()

	avmGenesis := &avm.Genesis{}
	if err = db.codec.Unmarshal(genesisBytes, avmGenesis); err != nil {
		return err
	}

	// Create db tx
	var dbTx *dbr.Tx
	dbTx, err = sess.Begin()
	if err != nil {
		return err
	}
	defer dbTx.RollbackUnlessCommitted()

	cCtx := services.NewConsumerContext(ctx, job, dbTx, timestamp)
	for _, tx := range avmGenesis.Txs {
		if err = db.ingestTx(cCtx, tx.Bytes()); err != nil {
			return err
		}
	}

	if err = dbTx.Commit(); err != nil {
		return err
	}

	return nil
}

// Index ingests a Transaction and adds it to the index
func (db *DB) Index(ctx context.Context, i services.Consumable) error {
	var (
		err  error
		job  = db.stream.NewJob("index")
		sess = db.db.NewSession(job)
	)
	job.KeyValue("id", i.ID())
	job.KeyValue("chain_id", i.ChainID())

	defer func() {
		if err != nil {
			job.CompleteKv(health.Error, health.Kvs{"err": err.Error()})
			return
		}
		job.Complete(health.Success)
	}()

	// Create db tx
	var dbTx *dbr.Tx
	dbTx, err = sess.Begin()
	if err != nil {
		return err
	}
	defer dbTx.RollbackUnlessCommitted()

	// Ingest the tx and commit
	if err = db.ingestTx(services.NewConsumerContext(ctx, job, dbTx, i.Timestamp()), i.Body()); err != nil {
		return err
	}

	if err = dbTx.Commit(); err != nil {
		return err
	}

	return nil
}

func (db *DB) ingestTx(ctx services.ConsumerCtx, txBytes []byte) error {
	tx, err := parseTx(db.codec, txBytes)
	if err != nil {
		return err
	}

	var (
		errs   = wrappers.Errs{}
		baseTx *avm.BaseTx
		txType = index.TXTypeBase
	)

	// Finish processing with a type-specific ingestion routine
	switch castTx := tx.UnsignedTx.(type) {
	case *avm.GenesisAsset:
		txType = index.TXTypeGenesisAsset
		baseTx = &castTx.BaseTx
		errs.Add(db.ingestCreateAssetTx(ctx, &castTx.CreateAssetTx, castTx.Alias))
	case *avm.CreateAssetTx:
		txType = index.TXTypeCreateAsset
		baseTx = &castTx.BaseTx
		errs.Add(db.ingestCreateAssetTx(ctx, castTx, ""))
	case *avm.OperationTx:
		txType = index.TXTypeOperation
		baseTx = &castTx.BaseTx
	case *avm.ImportTx:
		txType = index.TXTypeImport
		baseTx = &castTx.BaseTx
		castTx.BaseTx.Ins = append(castTx.BaseTx.Ins, castTx.Ins...)
	case *avm.ExportTx:
		txType = index.TXTypeExport
		baseTx = &castTx.BaseTx
		castTx.BaseTx.Outs = append(castTx.BaseTx.Outs, castTx.Outs...)
	case *avm.BaseTx:
		baseTx = castTx
	default:
		return errors.New("unknown tx type")
	}

	errs.Add(index.IngestBaseTx(ctx, tx.UnsignedBytes(), &baseTx.BaseTx, txType, tx.Credentials()))

	return errs.Err
}

func (db *DB) ingestCreateAssetTx(ctx services.ConsumerCtx, tx *avm.CreateAssetTx, alias string) error {
	wrappedTxBytes, err := db.codec.Marshal(&avm.Tx{UnsignedTx: tx})
	if err != nil {
		return err
	}
	txID := ids.NewID(hashing.ComputeHash256Array(wrappedTxBytes))

	var outputCount uint32
	var amount uint64
	for _, state := range tx.States {
		for _, out := range state.Outs {
			outputCount++

			xOut, ok := out.(*secp256k1fx.TransferOutput)
			if !ok {
				_ = ctx.Job().EventErr("assertion_to_secp256k1fx_transfer_output", errors.New("output is not a *secp256k1fx.TransferOutput"))
				continue
			}

			// TODO
			// index.IngestTxOutput(ctx, tx, outputCount-1, avax.TransferableOutput{
			// 	Out: out,
			// })

			amount, err = math.Add64(amount, xOut.Amount())
			if err != nil {
				_ = ctx.Job().EventErr("add_to_amount", err)
				continue
			}
		}
	}

	_, err = ctx.DB().
		InsertInto("avm_assets").
		Pair("id", txID.String()).
		Pair("chain_Id", db.chainID).
		Pair("name", tx.Name).
		Pair("symbol", tx.Symbol).
		Pair("denomination", tx.Denomination).
		Pair("alias", alias).
		Pair("current_supply", amount).
		ExecContext(ctx.Ctx())
	if err != nil && !index.IsDuplicateEntryError(err) {
		return err
	}
	return nil
}

func parseTx(c codec.Codec, bytes []byte) (*avm.Tx, error) {
	tx := &avm.Tx{}
	err := c.Unmarshal(bytes, tx)
	if err != nil {
		return nil, err
	}
	unsignedBytes, err := c.Marshal(&tx.UnsignedTx)
	if err != nil {
		return nil, err
	}

	tx.Initialize(unsignedBytes, bytes)
	return tx, nil

	// utx := &avm.UniqueTx{
	// 	TxState: &avm.TxState{
	// 		Tx: tx,
	// 	},
	// 	txID: tx.ID(),
	// }
	//
	// return utx, nil
}
