// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package consumers

import (
	"github.com/ava-labs/ortelius/cfg"
	"github.com/ava-labs/ortelius/services"
	"github.com/ava-labs/ortelius/services/avm_index"
	"github.com/ava-labs/ortelius/services/pvm_index"
	"github.com/ava-labs/ortelius/stream"
)

func NewIndexerFactory() stream.ProcessorFactory {
	return stream.NewConsumerFactory(createIndexerConsumer)
}

func createIndexerConsumer(conf cfg.ServiceConfig, networkID uint32, chainConfig cfg.ChainConfig) (indexer services.Consumer, err error) {
	switch chainConfig.VMType {
	case avm_index.VMName:
		indexer, err = avm_index.New(conf, networkID, chainConfig.ID)
	case pvm_index.VMName:
		indexer, err = pvm_index.New(conf, networkID, chainConfig.ID)
	default:
		return nil, stream.ErrUnknownVM
	}
	return indexer, err
}
