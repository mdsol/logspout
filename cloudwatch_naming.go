// Naming functions: These are CloudWatchManager functions for determining the
// LogGroup name and LogStream name for a given container ID

package main

import (
	"bytes"
	"log"
	"os"
	"strings"
	"text/template"
)

const DefaultLogGroup = `logspout-{{.Host}}`
const DefaultLogStream = `{{.Host}}-{{.Name}}`

// defines some data fields for rendering the Log Group name and the
// Log Stream name using the built-in golang template package
type NamingContext struct {
	ID   string
	Host string
	Name string
	Env  map[string]string
}

// returns a CloudWatch LogGroup name for a given container ID
func (cw *CloudWatchManager) getLogGroupName(ID string) string {
	groupNameTemplate := getopt("LOGSPOUT_GROUP", DefaultLogGroup)
	return cw.renderText(groupNameTemplate, ID)
}

// returns a CloudWatch LogStream name for a given container ID
func (cw *CloudWatchManager) getLogStreamName(ID string) string {
	streamNameTemplate := getopt("LOGSPOUT_STREAM", DefaultLogStream)
	return cw.renderText(streamNameTemplate, ID)
}

// renders the template text in a context derived from the given container ID
func (cw *CloudWatchManager) renderText(templateText, ID string) string {
	tmpl, err := template.New("logspout-cloudwatch").Parse(templateText)
	if err != nil {
		log.Println("WARN: Could not get parse name template:", templateText)
		return `logspout`
	}
	context, err := cw.getContext(ID)
	if err != nil {
		log.Println("ERROR: Could not get rendering context for container", ID)
		return `logspout`
	}
	buffer := bytes.NewBuffer([]byte{})
	err = tmpl.Execute(buffer, context)
	if err != nil {
		log.Println("WARN: Could not get render name template:", templateText)
		return `logspout`
	}
	return buffer.String()
}

// returns a NamingContext struct derived the hostname and the desired container
func (cw *CloudWatchManager) getContext(ID string) (*NamingContext, error) {
	hostname, err := os.Hostname()
	if err != nil {
		log.Println("ERROR: could determine hostname!")
		return nil, err
	}
	container, err := cw.docker.InspectContainer(ID)
	if err != nil {
		log.Println("ERROR: could not inspect container", ID)
		return nil, err
	}
	context := NamingContext{
		ID:   ID,
		Host: hostname,
		Name: strings.TrimLeft(container.Name, `/`),
		Env:  cw.getEnvMap(container.Config.Env),
	}
	return &context, nil
}

// returns a proper map from an array of strings of the form "KEY=VALUE"
func (cw *CloudWatchManager) getEnvMap(envStrings []string) map[string]string {
	env := map[string]string{}
	for _, line := range envStrings {
		matches := strings.SplitN(line, `=`, 2)
		if len(matches) > 1 {
			env[matches[0]] = matches[1]
		}
	}
	return env
}
