// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package avm

import (
	"context"
	"errors"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/database/nodb"
	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/utils/codec"
	"github.com/ava-labs/avalanchego/utils/hashing"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/math"
	"github.com/ava-labs/avalanchego/vms/avm"
	"github.com/ava-labs/avalanchego/vms/nftfx"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/health"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/ava-labs/ortelius/services"
	"github.com/ava-labs/ortelius/services/indexes/avax"
	"github.com/ava-labs/ortelius/services/indexes/models"
)

const (
	// MaxSerializationLen is the maximum number of bytes a canonically
	// serialized tx can be stored as in the database.
	MaxSerializationLen = 64000

	// MaxMemoLen is the maximum number of bytes a memo can be in the database
	MaxMemoLen = 2048
)

var (
	ErrIncorrectGenesisChainTxType = errors.New("incorrect genesis chain tx type")
)

type Writer struct {
	db        *services.DB
	chainID   string
	networkID uint32
	codec     codec.Codec
	stream    *health.Stream

	avax *avax.Writer
}

func NewWriter(conns *services.Connections, networkID uint32, chainID string) (*Writer, error) {
	avmCodec, err := newAVMCodec(networkID, chainID)
	if err != nil {
		return nil, err
	}

	return &Writer{
		codec:     avmCodec,
		chainID:   chainID,
		networkID: networkID,
		stream:    conns.Stream(),
		db:        services.NewDB(conns.Stream(), conns.DB()),
		avax:      avax.NewWriter(chainID, conns.Stream()),
	}, nil
}

func (*Writer) Name() string { return "avm-index" }

func (w *Writer) Bootstrap(ctx context.Context) error {
	platformGenesisBytes, _, err := genesis.Genesis(w.networkID)
	if err != nil {
		return err
	}

	platformGenesis := &platformvm.Genesis{}
	if err = platformvm.Codec.Unmarshal(platformGenesisBytes, platformGenesis); err != nil {
		return err
	}
	if err = platformGenesis.Initialize(); err != nil {
		return err
	}

	for _, chain := range platformGenesis.Chains {
		createChainTx, ok := chain.UnsignedTx.(*platformvm.UnsignedCreateChainTx)
		if !ok {
			return ErrIncorrectGenesisChainTxType
		}
		if createChainTx.VMID.Equals(avm.ID) {
			return w.ingestCreateChainTx(ctx, createChainTx, int64(platformGenesis.Timestamp))
		}
	}
	return nil
}

func (w *Writer) Consume(ctx context.Context, i services.Consumable) error {
	var (
		err  error
		job  = w.stream.NewJob("index")
		sess = w.db.NewSessionForEventReceiver(job)
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
	err = w.ingestTx(services.NewConsumerContext(ctx, job, dbTx, i.Timestamp()), i.Body())
	if err != nil {
		return err
	}

	err = dbTx.Commit()
	if err != nil {
		return err
	}

	return nil
}

func (w *Writer) Close(_ context.Context) error {
	w.stream.Event("close")
	return nil
}

func (w *Writer) ingestTx(ctx services.ConsumerCtx, txBytes []byte) error {
	tx, err := parseTx(w.codec, txBytes)
	if err != nil {
		return err
	}

	unsignedBytes, err := w.codec.Marshal(&tx.UnsignedTx)
	if err != nil {
		return err
	}

	// Finish processing with a type-specific ingestion routine
	switch castTx := tx.UnsignedTx.(type) {
	case *avm.GenesisAsset:
		return w.ingestCreateAssetTx(ctx, txBytes, &castTx.CreateAssetTx, castTx.Alias)
	case *avm.CreateAssetTx:
		return w.ingestCreateAssetTx(ctx, txBytes, castTx, "")
	case *avm.OperationTx:
		// 	db.ingestOperationTx(ctx, tx)
	case *avm.ImportTx:
		return w.avax.InsertTransaction(ctx, txBytes, unsignedBytes, &castTx.BaseTx.BaseTx, tx.Credentials(), models.TransactionTypeAVMImport)
	case *avm.ExportTx:
		return w.avax.InsertTransaction(ctx, txBytes, unsignedBytes, &castTx.BaseTx.BaseTx, tx.Credentials(), models.TransactionTypeAVMExport)
	case *avm.BaseTx:
		return w.avax.InsertTransaction(ctx, txBytes, unsignedBytes, &castTx.BaseTx, tx.Credentials(), models.TransactionTypeBase)
	default:
		return errors.New("unknown tx type")
	}
	return nil
}

func (w *Writer) ingestCreateChainTx(ctx context.Context, tx *platformvm.UnsignedCreateChainTx, timestamp int64) error {
	var (
		err  error
		job  = w.stream.NewJob("bootstrap")
		sess = w.db.NewSessionForEventReceiver(job)
	)
	job.KeyValue("chain_id", w.chainID)

	defer func() {
		if err != nil {
			job.CompleteKv(health.Error, health.Kvs{"err": err.Error()})
			return
		}
		job.Complete(health.Success)
	}()

	avmGenesis := &avm.Genesis{}
	if err = w.codec.Unmarshal(tx.GenesisData, avmGenesis); err != nil {
		return err
	}

	// Create db tx
	var dbTx *dbr.Tx
	dbTx, err = sess.Begin()
	if err != nil {
		return err
	}
	defer dbTx.RollbackUnlessCommitted()

	var txBytes []byte
	cCtx := services.NewConsumerContext(ctx, job, dbTx, timestamp)
	for _, tx := range avmGenesis.Txs {
		txBytes, err = w.codec.Marshal(tx)
		if err != nil {
			return err
		}
		err = w.ingestCreateAssetTx(cCtx, txBytes, &tx.CreateAssetTx, tx.Alias)
		if err != nil {
			return err
		}
	}

	err = dbTx.Commit()
	if err != nil {
		return err
	}

	return nil
}

func (w *Writer) ingestCreateAssetTx(ctx services.ConsumerCtx, txBytes []byte, tx *avm.CreateAssetTx, alias string) error {
	wrappedTxBytes, err := w.codec.Marshal(&avm.Tx{UnsignedTx: tx})
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

			w.avax.InsertOutput(ctx, txID, outputCount-1, txID, xOut, true)

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
		Pair("chain_Id", w.chainID).
		Pair("name", tx.Name).
		Pair("symbol", tx.Symbol).
		Pair("denomination", tx.Denomination).
		Pair("alias", alias).
		Pair("current_supply", amount).
		ExecContext(ctx.Ctx())
	if err != nil && !services.ErrIsDuplicateEntryError(err) {
		return err
	}

	// If the tx or memo is too big we can't store it in the db
	if len(txBytes) > MaxSerializationLen {
		txBytes = []byte{}
	}

	if len(tx.Memo) > MaxMemoLen {
		tx.Memo = nil
	}

	_, err = ctx.DB().
		InsertInto("avm_transactions").
		Pair("id", txID.String()).
		Pair("chain_id", w.chainID).
		Pair("type", models.TransactionTypeCreateAsset).
		Pair("memo", tx.Memo).
		Pair("created_at", ctx.Time()).
		Pair("canonical_serialization", txBytes).
		ExecContext(ctx.Ctx())
	if err != nil && !services.ErrIsDuplicateEntryError(err) {
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

// newAVMCodec creates codec that can parse avm objects
func newAVMCodec(networkID uint32, chainID string) (codec.Codec, error) {
	g, err := genesis.VMGenesis(networkID, avm.ID)
	if err != nil {
		return nil, err
	}

	createChainTx, ok := g.UnsignedTx.(*platformvm.UnsignedCreateChainTx)
	if !ok {
		return nil, ErrIncorrectGenesisChainTxType
	}

	bcLookup := &ids.Aliaser{}
	bcLookup.Initialize()
	id, err := ids.FromString(chainID)
	if err != nil {
		return nil, err
	}
	if err = bcLookup.Alias(id, "X"); err != nil {
		return nil, err
	}

	var (
		fxIDs = createChainTx.FxIDs
		fxs   = make([]*common.Fx, 0, len(fxIDs))
		ctx   = &snow.Context{
			NetworkID: networkID,
			ChainID:   id,
			Log:       logging.NoLog{},
			Metrics:   prometheus.NewRegistry(),
			BCLookup:  bcLookup,
		}
	)
	for _, fxID := range fxIDs {
		switch {
		case fxID.Equals(secp256k1fx.ID):
			fxs = append(fxs, &common.Fx{
				Fx: &secp256k1fx.Fx{},
				ID: fxID,
			})
		case fxID.Equals(nftfx.ID):
			fxs = append(fxs, &common.Fx{
				Fx: &nftfx.Fx{},
				ID: fxID,
			})
		default:
			// return nil, fmt.Errorf("Unknown FxID: %s", fxID)
		}
	}

	// Initialize an producer to use for tx parsing
	// An error is returned about the DB being closed but this is expected because
	// we're not using a real DB here.
	vm := &avm.VM{}
	err = vm.Initialize(ctx, &nodb.Database{}, createChainTx.GenesisData, make(chan common.Message, 1), fxs)
	if err != nil && err != database.ErrClosed {
		return nil, err
	}

	return vm.Codec(), nil
}
