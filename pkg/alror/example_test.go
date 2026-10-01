package alror_test

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/manaskumar3003/alror-cli/internal/api/apitest"
	"github.com/manaskumar3003/alror-cli/pkg/alror"
)

// A local fake workspace stands in for a real server here. In your code,
// use your server's URL and an API key created in the console.
func ExampleNewClient() {
	_, srv := apitest.Start()
	defer srv.Close()

	client := alror.NewClient(srv.URL, apitest.DefaultKey)
	me, err := client.WhoAmI(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(me.Org.Slug, me.Actor.Type)
	fmt.Println(me.Scopes)
	// Output:
	// acme api_key
	// [deploy:read deploy:write jobs:run config:write]
}

func ExampleClient_EnqueueDeploy() {
	_, srv := apitest.Start()
	defer srv.Close()
	client := alror.NewClient(srv.URL, apitest.DefaultKey)

	job, err := client.EnqueueDeploy(context.Background(), alror.DeployRequest{
		Service:      "checkout-api",
		Image:        "registry.example.com/checkout:1.42",
		Ref:          "#4821",
		Environment:  "production",
		RiskOverride: "low", // skip scoring: runners have no git context
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(job.Kind, job.Status)
	// Output: deploy queued
}

func ExampleClient_GetDeployment() {
	_, srv := apitest.Start()
	defer srv.Close()
	client := alror.NewClient(srv.URL, apitest.DefaultKey)

	_, err := client.GetDeployment(context.Background(), "dep_does_not_exist")
	fmt.Println(errors.Is(err, alror.ErrNotFound))
	// Output: true
}
