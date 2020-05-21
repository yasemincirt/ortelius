// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ava-labs/ortelius/api"
	"github.com/ava-labs/ortelius/cfg"
	"github.com/ava-labs/ortelius/stream"
	"github.com/ava-labs/ortelius/stream/consumers"

	// Register service plugins
	_ "github.com/ava-labs/ortelius/services/avm_index"
)

const (
	rootCmdUse  = "orteliusd [command]\nex: orteliusd api"
	rootCmdDesc = "Daemons for Ortelius."

	apiCmdUse  = "api"
	apiCmdDesc = "Runs the API daemon"

	streamCmdUse  = "stream"
	streamCmdDesc = "Runs stream commands"

	streamProducerCmdUse  = "producer"
	streamProducerCmdDesc = "Runs the stream producer daemon"

	streamIndexerCmdUse  = "indexer"
	streamIndexerCmdDesc = "Runs the stream indexer daemon"

	streamReplayerCmdUse  = "replayer"
	streamReplayerCmdDesc = "Runs the stream replayer daemon"
)

// listenCloser listens for messages until it's asked to close
type listenCloser interface {
	Listen() error
	Close() error
}

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// Execute runs the root command for ortelius
func execute() error {
	var (
		rootCmd = &cobra.Command{Use: rootCmdUse, Short: rootCmdDesc, Long: rootCmdDesc}

		runErr error

		newStrPtr = func() *string { s := ""; return &s }

		configFile = newStrPtr()
		startTime  = newStrPtr()

		apiCmd = &cobra.Command{
			Use:   apiCmdUse,
			Short: apiCmdDesc,
			Long:  apiCmdDesc,
			Run: func(cmd *cobra.Command, args []string) {
				if len(args) == 0 {
					cmd.Help()
					os.Exit(0)
				}

				var config cfg.APIConfig
				var lc listenCloser
				if config, runErr = cfg.NewAPIConfig(*configFile); runErr != nil {
					return
				}
				if lc, runErr = api.NewServer(config); runErr != nil {
					return
				}
				runListenCloser(lc)
			},
		}

		streamCmd = &cobra.Command{
			Use:   streamCmdUse,
			Short: streamCmdDesc,
			Long:  streamCmdDesc,
			Run: func(cmd *cobra.Command, args []string) {
				if len(args) == 0 {
					cmd.Help()
					os.Exit(0)
				}
			},
		}
	)

	// Add flags and commands
	rootCmd.PersistentFlags().StringVarP(configFile, "config", "c", "config.json", "")
	streamCmd.PersistentFlags().StringVar(startTime, "starttime", "startTime", "")

	rootCmd.AddCommand(apiCmd)
	rootCmd.AddCommand(streamCmd)

	// Add stream sub commands
	streamCmd.AddCommand(&cobra.Command{
		Use:   streamProducerCmdUse,
		Short: streamProducerCmdDesc,
		Long:  streamProducerCmdDesc,
		Run:   streamProcessorCmdRunFn(configFile, startTime, &runErr, stream.NewProducer),
	})

	streamCmd.AddCommand(&cobra.Command{
		Use:   streamIndexerCmdUse,
		Short: streamIndexerCmdDesc,
		Long:  streamIndexerCmdDesc,
		Run:   streamProcessorCmdRunFn(configFile, startTime, &runErr, consumers.NewIndexerFactory()),
	})

	streamCmd.AddCommand(&cobra.Command{
		Use:   streamReplayerCmdUse,
		Short: streamReplayerCmdDesc,
		Long:  streamReplayerCmdDesc,
		Run:   streamProcessorCmdRunFn(configFile, startTime, &runErr, consumers.NewReplayerFactory()),
	})

	// Execute the command and return the runErr to the caller
	if err := rootCmd.Execute(); err != nil {
		return err
	}
	return runErr
}

// runListenCloser runs the listenCloser until signaled to stop
func runListenCloser(lc listenCloser) {
	// Start listening in the background
	go func() {
		if err := lc.Listen(); err != nil {
			log.Fatalln("Daemon listen error:", err.Error())
		}
	}()

	// Wait for exit signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	<-sigCh

	// Stop server
	if err := lc.Close(); err != nil {
		log.Fatalln("Daemon shutdown error:", err.Error())
	}
}

func streamProcessorCmdRunFn(configFile *string, startTime *string, runErr *error, factory stream.ProcessorFactory) func(_ *cobra.Command, _ []string) {
	return func(_ *cobra.Command, _ []string) {
		config, err := cfg.NewClientConfig(*configFile)
		if err != nil {
			*runErr = err
			return
		}

		// Add start time flag to config
		if startTime != nil && *startTime != "" {
			ts, err := strconv.Atoi(*startTime)
			if err != nil {
				*runErr = err
				return
			}
			config.StartTime = time.Unix(int64(ts), 0)
		}

		// Create and start processor
		processor, err := stream.NewProcessorManager(config, factory)
		if err != nil {
			*runErr = err
			return
		}
		runListenCloser(processor)
	}
}
