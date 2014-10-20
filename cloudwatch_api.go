// CloudWatch LogStream functions: These are CloudWatchManager functions for
// creating LogGroups and LogStreams on AWS as needed

package main

import (
	"fmt"
	"github.com/benton/goamz/aws"
	"github.com/benton/goamz/cloudwatch/logs"
	"log"
	"strings"
	"time"
)

const authTimeout = 365 //days

// sets up the AWS connection - returns and error, or nil on success
func (cw *CloudWatchManager) setupAWS(target Target) error {
	cw.Lock()
	defer cw.Unlock()
	var region aws.Region
	var exists bool
	if region, exists = aws.Regions[target.Addr]; !exists {
		region = aws.Region{
			Name: `unknown`,
			CloudWatchLogsEndpoint: "https://" + target.Addr,
		}
	}
	if target.Addr == `auto` {
		region = aws.Regions[cw.detectAWSRegionName()]
	}
	log.Println("routing logs to", region.CloudWatchLogsEndpoint)
	auth, err := aws.GetAuth("", "", "", time.Now().Add(authTimeout*24*time.Hour))
	if err != nil {
		log.Println("ERROR: reading AWS credentials", err)
		return err
	}
	if cw.aws == nil { // first-time AWS setup - start listening for events
		defer func() { // (once AWS client setup is complete)
			go cw.listenForContainerEvents(cw.attacher)
			go cw.runSweeper()
		}()
	}
	cw.aws = logs.New(auth, region)
	return nil
}

// returns the auto-detected AWS region name, or "us-east-1" if none is detected
func (cw *CloudWatchManager) detectAWSRegionName() string {
	log.Println("detecting AWS region...")
	// check environment var $AWS_REGION
	if regionName := getopt("AWS_REGION", ""); len(regionName) > 0 {
		debug("using ENV var", regionName)
		if _, exists := aws.Regions[regionName]; exists {
			return regionName
		}
		log.Printf("WARN: AWS region %s does not exist!", regionName)
	}
	// check EC2 metadata URL
	log.Println("checking EC2 metadata...")
	zone, err := aws.GetMetaData(`placement/availability-zone`)
	if err == nil {
		log.Println("running in EC2 availability zone", zone)
		return strings.TrimRight(string(zone), `abcdefghiklmnopqrstuvwxyz`)
	}
	// fall back to default
	defaultRegion := "us-east-1"
	log.Println("No EC2 zone detected - defaulting to", defaultRegion)
	return defaultRegion
}

// returns true if the LogGroup with name groupName exists
func (cw *CloudWatchManager) groupExists(groupName string) bool {
	groupResult, err := cw.aws.DescribeLogGroups(groupName, 0, "")
	if err != nil {
		log.Println("ERROR: listing LogGroups", err)
		return false
	}
	groupExists := false
	for _, group := range groupResult.LogGroups {
		if group.LogGroupName == groupName {
			groupExists = true
		}
	}
	return groupExists
}

// creates a logGroup on AWS as needed
func (cw *CloudWatchManager) createGroup(groupName string) error {
	if cw.groupExists(groupName) == false {
		log.Println("Creating CloudWatch LogGroup", groupName)
		err := cw.aws.CreateLogGroup(groupName)
		if err != nil {
			return err
		}
	}
	return nil
}

// creates a LogStream on AWS as needed - returns the NextSequenceToken
func (cw *CloudWatchManager) createStream(streamName, groupName string) (
	string, error) {
	err := cw.createGroup(groupName)
	if err != nil {
		return "", err
	}
	streamResult, err := cw.aws.DescribeLogStreams(groupName, streamName, 0, "")
	if err != nil {
		log.Println("ERROR: listing LogStreams for group %s", groupName)
		return "", err
	}
	streamExists := false
	for _, stream := range streamResult.LogStreams {
		if stream.LogStreamName == streamName {
			streamExists = true
		}
	}
	if !streamExists {
		debug(fmt.Sprintf(
			"Creating CloudWatch LogStream %s/%s", groupName, streamName))
		err := cw.aws.CreateLogStream(groupName, streamName)
		if err != nil {
			return "", err
		}
	} else {
		streamResult, err := cw.aws.DescribeLogStreams(groupName, streamName, 0, "")
		if err != nil {
			log.Println("ERROR: listing LogStreams for group %s: %s", groupName, err)
			return "", err
		}
		for _, stream := range streamResult.LogStreams {
			if stream.LogStreamName == streamName {
				return stream.UploadSequenceToken, nil
			}
		}
	}
	return "", nil // fallthrough case for newly-created LogStreams
}

// Returns the NextSequenceToken for the given streamName and groupName.
// The group and stream must exist already on AWS.
func (cw *CloudWatchManager) getStreamToken(streamName, groupName string) (
	string, error) {
	token, err := cw.createStream(streamName, groupName)
	if err != nil {
		return "", err
	}
	return token, nil
}
