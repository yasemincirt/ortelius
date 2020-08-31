// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package index

import (
	"strings"

	"github.com/ava-labs/gecko/ids"
	"github.com/ava-labs/gecko/utils/crypto"
	"github.com/ava-labs/gecko/utils/math"
	"github.com/ava-labs/gecko/utils/wrappers"
	"github.com/ava-labs/gecko/vms/components/avax"
	"github.com/ava-labs/gecko/vms/components/verify"
	"github.com/ava-labs/gecko/vms/secp256k1fx"
	"github.com/gocraft/dbr"

	"github.com/ava-labs/ortelius/services"
)

const (
	// MaxSerializationLen is the maximum number of bytes a canonically
	// serialized tx can be stored as in the database.
	MaxSerializationLen = 64000
)

func IsDuplicateEntryError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "Error 1062: Duplicate entry")
}

func IngestBaseTx(ctx services.ConsumerCtx, unsignedBytes []byte, baseTx *avax.BaseTx, creds []verify.Verifiable) error {
	var (
		err   error
		total uint64 = 0
	)

	redeemedOutputs := make([]string, 0, 2*len(baseTx.Ins))
	for i, in := range baseTx.Ins {
		total, err = math.Add64(total, in.Input().Amount())
		if err != nil {
			return err
		}

		inputID := in.TxID.Prefix(uint64(in.OutputIndex))

		// Save id so we can mark this output as consumed
		redeemedOutputs = append(redeemedOutputs, inputID.String())

		// Upsert this input as an output in case we haven't seen the parent tx
		// IngestTxOutput(ctx, *baseTx, in.UTXOID.OutputIndex, avax.TransferableOutput{
		// 	Asset: avax.Asset{ID: in.AssetID()},
		// 	Out: &secp256k1fx.TransferOutput{
		// 		Amt: in.In.Amount(),
		// 		OutputOwners: secp256k1fx.OutputOwners{
		// 			// We leave Addrs blank because we ingested them above with their signatures
		// 			Addrs: []ids.ShortID{},
		// 		},
		// 	},
		// })

		// For each signature we recover the public key and the data to the db
		cred, ok := creds[i].(*secp256k1fx.Credential)
		if !ok {
			return nil
		}
		for _, sig := range cred.Sigs {
			publicKey, err := ECDSAFactory().RecoverPublicKey(unsignedBytes, sig[:])
			if err != nil {
				return err
			}

			ingestAddressFromPublicKey(ctx, publicKey)
			ingestOutputAddress(ctx, inputID, publicKey.Address(), sig[:])
		}
	}

	// Mark all inputs as redeemed
	if len(redeemedOutputs) > 0 {
		_, err = ctx.DB().
			Update("avm_outputs").
			Set("redeemed_at", dbr.Now).
			Set("redeeming_transaction_id", baseTx.ID().String()).
			Where("id IN ?", redeemedOutputs).
			ExecContext(ctx.Ctx())
		if err != nil {
			return err
		}
	}

	// If the tx is too big we can't store it in the db
	if len(unsignedBytes) > MaxSerializationLen {
		unsignedBytes = []byte{}
	}

	// Add baseTx to the table
	_, err = ctx.DB().
		InsertInto("avm_transactions").
		Pair("id", baseTx.ID().String()).
		Pair("chain_id", baseTx.BlockchainID.String()).
		// Pair("type", txType).
		Pair("created_at", ctx.Time()).
		Pair("canonical_serialization", unsignedBytes).
		ExecContext(ctx.Ctx())
	if err != nil && !IsDuplicateEntryError(err) {
		// TODO: log error
	}

	// Process baseTx outputs by adding to the outputs table
	for idx, out := range baseTx.Outs {
		if err = IngestTxOutput(ctx, *baseTx, uint32(idx), out.AssetID(), out.Output()); err != nil {
			// TODO: log error
		}
	}
	return nil
}

func IngestTxOutput(ctx services.ConsumerCtx, tx avax.BaseTx, idx uint32, assetID ids.ID, out verify.State) error {
	// func IngestTxOutput(ctx services.ConsumerCtx, tx avax.BaseTx, idx uint32, out avax.TransferableOutput) error {
	errs := wrappers.Errs{}

	switch _ := out.(type) {
	case avax.TransferableOut:
	}

	id := (&avax.UTXOID{TxID: tx.ID(), OutputIndex: idx}).InputID()

	builder := ctx.DB().
		InsertInto("avm_outputs").
		Pair("id", id.String()).
		Pair("chain_id", tx.BlockchainID.String()).
		Pair("transaction_id", tx.ID().String()).
		Pair("output_index", idx).
		Pair("asset_id", assetID.String()).
		Pair("created_at", ctx.Time())

	// secp256k1fx-specific fields
	if secp256k1FxOut, ok := out.(*secp256k1fx.TransferOutput); ok {
		builder.Pair("output_type", OutputTypesSECP2556K1Transfer).
			Pair("amount", secp256k1FxOut.Amount()).
			Pair("locktime", secp256k1FxOut.Locktime).
			Pair("threshold", secp256k1FxOut.Threshold)

		// Ingest each Output Address
		for _, addr := range secp256k1FxOut.Addresses() {
			addrBytes := [20]byte{}
			copy(addrBytes[:], addr)
			errs.Add(ingestOutputAddress(ctx, id, ids.NewShortID(addrBytes), nil))
		}
	}

	if _, err := builder.ExecContext(ctx.Ctx()); err != nil && !IsDuplicateEntryError(err) {
		errs.Add(err)
	}
	return errs.Err
}

func ingestAddressFromPublicKey(ctx services.ConsumerCtx, publicKey crypto.PublicKey) error {
	_, err := ctx.DB().
		InsertInto("addresses").
		Pair("address", publicKey.Address().String()).
		Pair("public_key", publicKey.Bytes()).
		ExecContext(ctx.Ctx())
	if err != nil && !IsDuplicateEntryError(err) {
		return err
	}
	return nil
}

func ingestOutputAddress(ctx services.ConsumerCtx, outputID ids.ID, address ids.ShortID, sig []byte) error {
	builder := ctx.DB().
		InsertInto("avm_output_addresses").
		Pair("output_id", outputID.String()).
		Pair("address", address.String())

	if sig != nil {
		builder = builder.Pair("redeeming_signature", sig)
	}

	if _, err := builder.ExecContext(ctx.Ctx()); err != nil && !IsDuplicateEntryError(err) {
		return err
	}

	if sig == nil {
		return nil
	}

	_, err := ctx.DB().
		Update("avm_output_addresses").
		Set("redeeming_signature", sig).
		Where("output_id = ? and address = ?", outputID.String(), address.String()).
		ExecContext(ctx.Ctx())
	return err
}
