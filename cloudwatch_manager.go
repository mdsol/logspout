package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/benton/goamz/cloudwatch/logs"
	"github.com/fsouza/go-dockerclient"
)

// Manages and submits Batches of Log entries, on a schedule or when they're full
type CloudWatchManager struct {
	attacher   *AttachManager
	docker     *docker.Client
	aws        *logs.CloudWatchLogs
	batches    map[string]*Batch // maps each container ID to a Batch of events
	sync.Mutex                   // protects access to the preceeding map
}

// Returns a pointer to a new, fully-initialized CloudWatchManager
func NewCloudWatchManager(attacher *AttachManager) *CloudWatchManager {
	return &CloudWatchManager{
		docker:   attacher.client,
		attacher: attacher,
		batches:  map[string]*Batch{},
	}
}

// Receives container attach/detach events and invokes the correct function.
// Invoked in a separate goroutine by NewCloudWatchManager().
func (cw *CloudWatchManager) listenForContainerEvents(attacher *AttachManager) {
	events := make(chan *AttachEvent)
	defer close(events)
	attacher.addListener(events)
	defer attacher.removeListener(events)
	for {
		select {
		case event := <-events:
			debug(fmt.Sprintf("Got container event %v", event))
			switch event.Type {
			case "attach":
				cw.HandleAttachEvent(event)
			case "detach":
				cw.HandleDetachEvent(event)
			}
		}
	}
}

// Responds to a container attach event by creating a new Batch for the container.
func (cw *CloudWatchManager) HandleAttachEvent(event *AttachEvent) {
	cw.Lock()
	defer cw.Unlock()
	group := cw.getLogGroupName(event.ID)
	stream := cw.getLogStreamName(event.ID)
	if cw.batches[event.ID] == nil {
		sequenceToken, err := cw.getStreamToken(stream, group) // makes new Batch
		if err != nil {
			log.Printf(
				"ERROR: getting SequenceUploadToken from stream %s/%s: %v",
				group, stream, err)
		}
		cw.batches[event.ID] = &Batch{
			GroupName:  group,
			StreamName: stream,
			Token:      sequenceToken,
		}
	}
}

// Responds to a container attach event by submitting the Batch for the
// container, then deleting it.
func (cw *CloudWatchManager) HandleDetachEvent(event *AttachEvent) {
	cw.Lock()
	defer cw.Unlock()
	err := cw.submitBatchForID(event.ID)
	if err != nil { // error on batch submission - drop this batch
		log.Printf("ERROR: submitting batch for container %s: %v",
			event.ID, err)
	}
	delete(cw.batches, event.ID) // dereference for garbage collection
}

// Submits a Batch to AWS and replaces it with a new one.
// Lock the CloudWatchManager before invoking this function,
// then Unlock it soon thereafter!
func (cw *CloudWatchManager) submitBatchForID(ID string) (err error) {
	batch := cw.batches[ID]
	if len(batch.logs) > 0 {
		nextToken, err := cw.aws.PutLogEvents(
			batch.logs, batch.GroupName, batch.StreamName, batch.Token)
		if err != nil {
			return err
		}
		debug(fmt.Sprintf(
			"Submitted batch of %d events for container %s to %s/%s",
			len(batch.logs), ID, batch.GroupName, batch.StreamName))
		cw.batches[ID] = &Batch{
			GroupName:  batch.GroupName,
			StreamName: batch.StreamName,
			Token:      nextToken,
		}
	}
	return nil
}

// Responds to an emitted log line, by adding it to the correct container's Batch.
// Submits and replaces the Batch first if the message won't fit.
// Invoked from the "logging" goroutine that is assigned to each container.
func (cw *CloudWatchManager) HandleLogEvent(target *Target, logEvent *Log) {
	// log.Printf("Got log event %v for target %v\n", logEvent, target)
	cw.Lock()
	defer cw.Unlock()
	batch := cw.batches[logEvent.ID]
	batch.Lock() // lock the batch while we manipulate or submit it
	defer batch.Unlock()
	// If this logEvent message fits in the current batch, then add it
	if batch.messageFits(logEvent) {
		batch.AddEvent(logEvent)
	} else { // full batch - submit it, then add the event to the new batch
		err := cw.submitBatchForID(logEvent.ID)
		if err != nil { // error on batch submission - drop this event
			debug(fmt.Sprintf("ERROR: submitting full batch for container %s: %v",
				logEvent.ID, err))
			return
		}
		// now batches[ID] contains a new, empty batch, so add the current event
		newBatch := cw.batches[logEvent.ID]
		newBatch.Lock() // lock the batch while we add the current event
		defer newBatch.Unlock()
		newBatch.AddEvent(logEvent)
	}
}

// Loops forever, running sweepForOldBatches() every maxBatchAge seconds.
// Invoked in a separate goroutine by NewCloudWatchManager().
func (cw *CloudWatchManager) runSweeper() {
	for { // loop forever - but wait for maxBatchAge seconds between checks
		time.Sleep(maxBatchAge * time.Second)
		cw.sweepForOldBatches()
	}
}

// Submits all (non-empty) batches.
// Invoked in a separate goroutine by runSweeper().
func (cw *CloudWatchManager) sweepForOldBatches() {
	cw.Lock()
	defer cw.Unlock()
	for ID, _ := range cw.batches {
		cw.submitBatchForID(ID)
	}
}
