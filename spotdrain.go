package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/smithy-go"
	awshttp "github.com/aws/smithy-go/transport/http"

	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/DataDog/datadog-api-client-go/v2/api/datadog"
	ddapi "github.com/DataDog/datadog-api-client-go/v2/api/datadogV1"
)

// Global channel used to gracefully stop ticker
var done = make(chan bool)

func main() {
	imdsClient := createImdsClient()
	nomadClient := createNomadClient()
	ddClient, ddCtx, ddEnv := createDataDogClient()

	if !isSpotInstance(imdsClient) {
		log.Print("This is not a spot instance. Spotdrain will not run")
		log.Print("Exiting ...")
		os.Exit(0)
	}

	instanceId := getEC2InstanceId(imdsClient)
	registered, nodeId := checkNodeRegistered(nomadClient, instanceId)
	if !registered {
		log.Print("This instance is not registered on Nomad. Spotdrain will not run")
		log.Print("Exiting...")
		os.Exit(1)
	}

	ticker := time.NewTicker(10 * time.Second)

	go func() {
		for {
			select {
			case <-done:
				log.Print("Stopping ticker")
				return
			case <-ticker.C:
				log.Print("Waking up and executing check")
				if checkMarkedForInterruption(imdsClient) {
					triggerNomadNodeDrain(nomadClient, nodeId)
					sendDatadogEvent(ddClient, ddCtx, instanceId, ddEnv)
					log.Print("Job's done. Exiting...")
					os.Exit(0)
				}
			}
		}

	}()

	// Block so we don't exit
	select {}
}

func init() {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Print("Stopping ...")
		done <- true
		// Wait half a second for the ticker to properly close
		time.Sleep(500 * time.Millisecond)
		os.Exit(155)
	}()
}

func createNomadClient() *nomadapi.Client {
	nomadToken := getNomadTokenFromEnv()
	config := nomadapi.DefaultConfig()
	config.Address = "https://127.0.0.1:4646"
	config.Headers = make(http.Header)
	config.Headers.Add("X-Nomad-Token", nomadToken)
	client, err := nomadapi.NewClient(config)
	if err != nil {
		log.Fatal("Fatal: ", err)
	}
	return client
}

func getNomadTokenFromEnv() string {
	token, exists := os.LookupEnv("SPOTDRAIN_NOMAD_TOKEN")
	if !exists {
		log.Fatal("Fatal: Nomad Auth Token not present in environment variable SPOTDRAIN_NOMAD_TOKEN")
	}
	return token
}

func createImdsClient() *imds.Client {
	return imds.New(imds.Options{})
}

func createDataDogClient() (*datadog.APIClient, context.Context, string) {
	ctx := context.WithValue(
		context.Background(),
		datadog.ContextAPIKeys,
		map[string]datadog.APIKey{
			"apiKeyAuth": {
				Key: os.Getenv("DD_CLIENT_API_KEY"),
			},
			"appKeyAuth": {
				Key: os.Getenv("DD_CLIENT_APP_KEY"),
			},
		},
	)

	conf := datadog.NewConfiguration()
	client := datadog.NewAPIClient(conf)
	env, exists := os.LookupEnv("DD_ENV")
	if !exists {
		log.Print("WARN: Datadog env not found. Event will not have env tag")
	}
	return client, ctx, env
}

func isSpotInstance(client *imds.Client) bool {
	mdi := &imds.GetMetadataInput{Path: "/instance-life-cycle"}
	output, err := client.GetMetadata(context.TODO(), mdi)
	if err != nil {
		log.Fatal("Fatal: ", err)
	}

	bytes, err := ioutil.ReadAll(output.Content)
	if err != nil {
		log.Fatal("Fatal: ", err)
	}

	return strings.EqualFold(string(bytes), "spot")
}

func getEC2InstanceId(client *imds.Client) string {
	mdi := &imds.GetMetadataInput{Path: "/instance-id"}
	output, err := client.GetMetadata(context.TODO(), mdi)
	if err != nil {
		log.Fatal("Fatal: ", err)
	}

	bytes, err := ioutil.ReadAll(output.Content)
	if err != nil {
		log.Fatal("Fatal: ", err)
	}

	return string(bytes)
}

func checkMarkedForInterruption(client *imds.Client) bool {
	marked := false

	mdi := &imds.GetMetadataInput{Path: "/spot/instance-action"}
	output, err := client.GetMetadata(context.TODO(), mdi)
	if err != nil {
		var oe *smithy.OperationError
		if errors.As(err, &oe) {
			var re *awshttp.ResponseError
			if errors.As(oe.Unwrap(), &re) {
				if re.HTTPStatusCode() == 404 {
					log.Print("Metadata not available")
					return marked
				}
			}
		}
		log.Fatal("Fatal: ", err)
	}

	bytes, err := ioutil.ReadAll(output.Content)
	if err != nil {
		log.Fatal("Fatal: ", err)
	}

	var ia InstanceAction
	err = json.Unmarshal(bytes, &ia)
	if err != nil {
		log.Fatal("Fatal: ", err)
	}

	marked = true
	log.Printf("Instance is Marked for interruption! Action: %s, Time: %s", ia.Action, ia.Time)

	return marked
}

func checkNodeRegistered(client *nomadapi.Client, instanceId string) (bool, string) {
	nodeList, _, err := client.Nodes().List(&nomadapi.QueryOptions{})
	if err != nil {
		log.Fatal("Fatal: ", err)
	}
	for _, node := range nodeList {
		if strings.EqualFold(instanceId, node.Name) {
			log.Print("This host is registered on Nomad")
			return true, node.ID
		}
	}

	log.Print("Could not find this instance registered in Nomad")
	return false, ""
}
func triggerNomadNodeDrain(client *nomadapi.Client, nodeId string) {
	ds := &nomadapi.DrainSpec{Deadline: 60 * time.Second, IgnoreSystemJobs: true}
	wo := &nomadapi.WriteOptions{}
	_, err := client.Nodes().UpdateDrain(nodeId, ds, false, wo)
	if err != nil {
		log.Fatal("Fatal: Error triggering Nomad Node Drain: ", err)
	}
	log.Println("Triggered Nomad Node Drain")
}

func sendDatadogEvent(client *datadog.APIClient, ctx context.Context, instanceId string, ddEnv string) {
	tags := []string{"service:spotdrain", "spotdrain:termination_notice", "env:" + ddEnv}
	er := ddapi.NewEventCreateRequest("This instance has received a termination notice from AWS EC2", "Spot-Instance-Termination-Notice")
	er.SetPriority(ddapi.EVENTPRIORITY_NORMAL)
	er.SetAlertType(ddapi.EVENTALERTTYPE_WARNING)
	er.SetHost(instanceId)
	er.SetTags(tags)
	ecr, _, err := ddapi.NewEventsApi(client).CreateEvent(ctx, *er)
	if err != nil {
		log.Fatal("Fatal: Error creating datadog event: ", err)
	}
	log.Print("Sending Datadog event: ", ecr.GetStatus())
}

type InstanceAction struct {
	Action string    `json:"action"`
	Time   time.Time `json:"time"`
}
