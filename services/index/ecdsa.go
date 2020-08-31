// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package index

import (
	"github.com/ava-labs/gecko/utils/crypto"
)

var ecdsaFactory = &crypto.FactorySECP256K1R{}

func ECDSAFactory() *crypto.FactorySECP256K1R {
	return ecdsaFactory
}
