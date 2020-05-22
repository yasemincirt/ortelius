// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package consumers

import (
	"github.com/ava-labs/ortelius/cfg"
	"github.com/ava-labs/ortelius/services"
	"github.com/ava-labs/ortelius/stream"
)

func NewBroadcasterFactory() stream.ProcessorFactory {
	return stream.NewConsumerFactory(createBroadcasterConsumer)
}

func createBroadcasterConsumer(conf cfg.Config, networkID uint32, chainConfig cfg.Chain) (indexer services.Consumer, err error) {
	return services.NewBroadcaster(chainConfig), nil
}
