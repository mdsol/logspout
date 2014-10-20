package main

import (
	"sync"
	"time"

	"github.com/benton/goamz/cloudwatch/logs"
)

const maxBatchLength = 1000 // messages
const maxBatchSize = 32768  // bytes - see http://goo.gl/K6t6Y6
const maxBatchAge = 10      // seconds - submit any batches older than this

// models a batch of CloudWatch Log events from a single source
type Batch struct {
	GroupName  string
	StreamName string
	Token      string
	bytes      int64
	logs       []logs.InputLogEvent
	sync.Mutex
}

// defines the byte count for a LogMessage - see http://goo.gl/K6t6Y6
func (batch *Batch) messageSize(dockerLog *Log) int64 {
	return int64(len([]byte(dockerLog.Data))) + 28
}

// Adds a new log event to this batch.
// Lock the Batch before invoking this function, then Unlock it soon thereafter!
func (batch *Batch) AddEvent(dockerLog *Log) {
	now := time.Now().UnixNano() / 1000000 // AWS wants milliseconds in epoch
	batch.logs = append(batch.logs, logs.InputLogEvent{dockerLog.Data, now})
	batch.bytes += batch.messageSize(dockerLog)
}

// Returns true if the dockerLog message will fit in this batch.
// Lock the Batch before this test, then Unlock after the conditional block(s)!
func (batch *Batch) messageFits(dockerLog *Log) bool {
	newSize := batch.bytes + batch.messageSize(dockerLog)
	return (len(batch.logs) < maxBatchLength) && (newSize <= maxBatchSize)
}
