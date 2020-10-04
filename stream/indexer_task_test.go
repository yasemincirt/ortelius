package stream

import (
	"context"
	"testing"
	"time"

	"github.com/ava-labs/ortelius/services/indexes/models"

	"github.com/ava-labs/ortelius/services"
	"github.com/ava-labs/ortelius/services/indexes/params"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/health"
)

type EventReceiverTest struct {
}

func (*EventReceiverTest) Event(eventName string)                          {}
func (*EventReceiverTest) EventKv(eventName string, kvs map[string]string) {}
func (*EventReceiverTest) EventErr(eventName string, err error) error      { return nil }
func (*EventReceiverTest) EventErrKv(eventName string, err error, kvs map[string]string) error {
	return nil
}
func (*EventReceiverTest) Timing(eventName string, nanoseconds int64)                          {}
func (*EventReceiverTest) TimingKv(eventName string, nanoseconds int64, kvs map[string]string) {}

func TestIntegration(t *testing.T) {
	var eventReceiver EventReceiverTest

	c, err := dbr.Open("mysql", "root:password@tcp(127.0.0.1:3306)/ortelius_test?parseTime=true", &eventReceiver)
	if err != nil {
		t.Errorf("open db %s", err.Error())
	}

	h := health.NewStream()

	co := services.NewConnections(h, c, nil)

	// produce an expected timestamp to test..
	timenow := time.Now().Round(1 * time.Minute)
	timeProducerFunc := func() time.Time {
		return timenow
	}

	tasker := ProducerTasker{connections: co,
		avmOutputsCursor:        AvmOutputsAggregateCursor,
		insertAvmAggregate:      models.InsertAvmAssetAggregation,
		updateAvmAggregate:      models.UpdateAvmAssetAggregation,
		insertAvmAggregateCount: models.InsertAvmAssetAggregationCount,
		updateAvmAggregateCount: models.UpdateAvmAssetAggregationCount,
		timeStampProducer:       timeProducerFunc,
	}

	ctx := context.Background()

	job := co.Stream().NewJob("producertasker")
	sess := co.DB().NewSession(job)

	// cleanup for run.
	_, _ = models.DeleteAvmAssetAggregationState(ctx, sess, params.StateBackupID)
	_, _ = models.DeleteAvmAssetAggregationState(ctx, sess, params.StateLiveID)
	_, _ = sess.DeleteFrom("avm_asset_aggregation").ExecContext(ctx)
	_, _ = sess.DeleteFrom("avm_asset_address_counts").ExecContext(ctx)

	pastime := time.Now().Add(-5 * time.Hour).Round(1 * time.Minute).Add(1 * time.Second)

	_, _ = sess.InsertInto("avm_outputs").
		Pair("id", "id1").
		Pair("chain_id", "cid").
		Pair("output_index", 1).
		Pair("output_type", 1).
		Pair("locktime", 1).
		Pair("threshold", 1).
		Pair("created_at", pastime).
		Pair("asset_id", "testasset").
		Pair("amount", 100).
		Pair("transaction_id", 1).
		Exec()

	_, _ = sess.InsertInto("avm_outputs").
		Pair("id", "id2").
		Pair("chain_id", "cid").
		Pair("output_index", 1).
		Pair("output_type", 1).
		Pair("locktime", 1).
		Pair("threshold", 1).
		Pair("created_at", pastime).
		Pair("asset_id", "testasset").
		Pair("amount", 100).
		Pair("transaction_id", 1).
		Exec()

	_, _ = sess.InsertInto("avm_output_addresses").
		Pair("output_id", "id1").
		Pair("address", "addr1").
		Pair("created_at", pastime).
		Exec()

	avmAggregate := models.AvmAggregate{}
	avmAggregate.AggregateTS = time.Now().Add(time.Duration(aggregateDeleteFrame.Milliseconds()+1) * time.Millisecond)
	avmAggregate.AssetID = "futureasset"
	_, _ = models.InsertAvmAssetAggregation(ctx, sess, avmAggregate)

	err = tasker.RefreshAggregates()
	if err != nil {
		t.Errorf("refresh failed %s", err.Error())
	}

	backupAggregationState, _ := models.SelectAvmAssetAggregationState(ctx, sess, params.StateBackupID)
	liveAggregationState, _ := models.SelectAvmAssetAggregationState(ctx, sess, params.StateLiveID)
	if liveAggregationState.ID != params.StateLiveID {
		t.Errorf("state live not created")
	}
	if !liveAggregationState.CreatedAt.Equal(timenow.Add(additionalHours)) {
		t.Errorf("state live createdat not reset to the future")
	}
	if backupAggregationState.ID != 0 {
		t.Errorf("state backup not removed")
	}

	count := 999999
	_, _ = sess.Select("count(*)").From("avm_asset_aggregation").
		Where("aggregate_ts < ?", time.Now().Add(aggregateDeleteFrame)).
		Load(&count)
	if count != 0 {
		t.Errorf("future avm_asset not removed")
	}

	avmAggregateModels, _ := models.SelectAvmAssetAggregations(ctx, sess)

	for _, aggregateMapValue := range avmAggregateModels {
		if aggregateMapValue.AssetID != "testasset" &&
			aggregateMapValue.TransactionVolume != "200" &&
			aggregateMapValue.TransactionCount != 1 &&
			aggregateMapValue.AssetCount != 2 {
			t.Errorf("aggregate map invalid")
		}
	}

	avmAggregateCounts, _ := models.SelectAvmAssetAggregationCounts(ctx, sess)
	if len(avmAggregateCounts) != 1 {
		t.Errorf("aggregate map count not created")
	}

	for _, aggregateCountMapValue := range avmAggregateCounts {
		if aggregateCountMapValue.Address != "id1" &&
			aggregateCountMapValue.AssetID != "testasset" &&
			aggregateCountMapValue.TransactionCount != 1 &&
			aggregateCountMapValue.TotalSent != "0" &&
			aggregateCountMapValue.TotalReceived != "100" &&
			aggregateCountMapValue.Balance != "100" &&
			aggregateCountMapValue.UtxoCount != 1 {
			t.Errorf("aggregate map count invalid")
		}
	}
}

func TestHandleBackupState(t *testing.T) {
	var eventReceiver EventReceiverTest

	c, err := dbr.Open("mysql", "root:password@tcp(127.0.0.1:3306)/ortelius_test?parseTime=true", &eventReceiver)
	if err != nil {
		t.Errorf("open db %s", err.Error())
	}

	h := health.NewStream()

	co := services.NewConnections(h, c, nil)

	ctx := context.Background()

	job := co.Stream().NewJob("producertasker")
	sess := co.DB().NewSession(job)

	// cleanup for run.
	_, _ = models.DeleteAvmAssetAggregationState(ctx, sess, params.StateBackupID)
	_, _ = models.DeleteAvmAssetAggregationState(ctx, sess, params.StateLiveID)

	timeNow := time.Now().Round(1 * time.Minute)

	_, _ = models.InsertAvmAssetAggregationState(ctx, sess, models.AvmAssetAggregateState{
		ID:               params.StateBackupID,
		CreatedAt:        timeNow,
		CurrentCreatedAt: timeNow})

	state := models.AvmAssetAggregateState{
		ID:               params.StateLiveID,
		CreatedAt:        time.Unix(1, 0),
		CurrentCreatedAt: time.Unix(1, 0)}

	var producerTask ProducerTasker
	backupState, _ := producerTask.handleBackupState(ctx, sess, state)
	if backupState.ID != params.StateBackupID {
		t.Fatal("invalid state")
	}

	if !backupState.CurrentCreatedAt.Equal(state.CurrentCreatedAt) {
		t.Fatal("backup state current created not updated")
	}
}