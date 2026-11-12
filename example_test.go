package lra_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joernott/lra/v2"
)

type ClusterHealth struct {
	ClusterName string `json:"cluster_name"`
	Status      string `json:"status"`
}

func Example() {
	c, err := lra.New("https://elasticsearch.example.com:9200",
		lra.WithBasicAuth("admin", "1234"),
		lra.WithProxy("http://proxy.example.com:3128"),
		lra.WithTimeout(30*time.Second),
	)
	if err != nil {
		log.Fatal(err)
	}

	health, err := lra.GetJSON[ClusterHealth](context.Background(), c, "/_cluster/health")
	if se, ok := errors.AsType[*lra.StatusError](err); ok && se.StatusCode == http.StatusUnauthorized {
		log.Fatal("wrong credentials")
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(health.ClusterName, health.Status)
}

func ExampleWithTLSConfig() {
	// Trust the CA which signed the server certificate instead of disabling
	// certificate verification.
	pem, err := os.ReadFile("/etc/elasticsearch/certs/http_ca.crt")
	if err != nil {
		log.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		log.Fatal("no CA certificate found")
	}

	c, err := lra.New("https://elasticsearch.example.com:9200",
		lra.WithTLSConfig(&tls.Config{RootCAs: pool}),
	)
	if err != nil {
		log.Fatal(err)
	}
	_ = c
}

func ExamplePostJSON() {
	c, err := lra.New("https://api.example.com/v1")
	if err != nil {
		log.Fatal(err)
	}

	type user struct {
		ID   int    `json:"id,omitempty"`
		Name string `json:"name"`
	}
	created, err := lra.PostJSON[user](context.Background(), c, "/users", user{Name: "jane"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(created.ID)
}
