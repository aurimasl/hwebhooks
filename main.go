package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/streadway/amqp"
)

var (
	port     = flag.String("port", "443", "Listen port")
	certPath = flag.String("cert", "/etc/pki/tls/certs/hostinger.crt", "Certificate path")
	keyPath  = flag.String("key", "/etc/pki/tls/private/hostinger.key", "Private key path")
	queue    = flag.String("queue", "", "AMQP Queue to proxy requests to")
	config   = flag.String("config", "", "API config file")
	cfg      AmqpConfig
)

func init() {
	flag.Parse()
}

type HapiRequest struct {
	Type    string `json:"_type"`
	Method  string `json:"_method"`
	Hash    string `json:"hash"`
	Payload string `json:"payload"`
}

func handler(w http.ResponseWriter, r *http.Request) {
	rawBody, err := ioutil.ReadAll(r.Body)
	if err != nil {
		panic(err)
	}
	cType := r.Header.Get("Content-Type")

	var body []byte
	var payload map[string]interface{}

	switch cType {
	case "application/x-www-form-urlencoded":
		escaped, err := url.QueryUnescape(string(rawBody))
		if err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
		}
		body = []byte(escaped)
	case "application/json":
		body = rawBody

		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	var request HapiRequest
	request.Type = "hosting"
	request.Method = "webhooks_handle"
	request.Hash = r.URL.Path[len("/deploy/"):]
	request.Payload = string(body)
	req, _ := json.Marshal(request)

	log.Printf("Publishing payload to hapi.webhooks: %s", []byte(req))
	hapiPublish([]byte(req))
}

func main() {
	parseConfig()
	http.HandleFunc("/", handler)
	if *port == "443" {
		log.Fatal(http.ListenAndServeTLS(":443", *certPath, *keyPath, nil))
	} else {
		log.Fatal(http.ListenAndServe(":"+*port, nil))
	}
}

type AmqpConfig struct {
	Amqp struct {
		ConsumerTag string `json:"consumer_tag"`
		Exchange    string `json:"exchange"`
		Host        string `json:"host"`
		User        string `json:"user"`
		Pass        string `json:"pass"`
		Port        string `json:"port"`
		Queue       string `json:"queue"`
		Vhost       string `json:"vhost"`
	} `json:"amqp"`
}

func parseConfig() *AmqpConfig {
	raw, err := ioutil.ReadFile(*config)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	var c AmqpConfig
	err = json.Unmarshal(raw, &c)
	cfg = c
	return &c
}

func hapiPublish(payload []byte) {
	amqpURI := fmt.Sprintf(
		"amqp://%s:%s@%s:%s/%s",
		cfg.Amqp.User,
		cfg.Amqp.Pass,
		cfg.Amqp.Host,
		cfg.Amqp.Port,
		cfg.Amqp.Vhost,
	)
	conn, err := amqp.Dial(amqpURI)
	if err != nil {
		log.Printf("connection.open: %s", err)
		return
	}

	defer conn.Close()

	c, err := conn.Channel()
	if err != nil {
		log.Printf("channel.open: %s", err)
		return
	}

	err = c.ExchangeDeclare("logs", "topic", true, false, false, false, nil)
	if err != nil {
		log.Printf("exchange.declare: %v", err)
		return
	}

	msg := amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		ContentType:  "application/json",
		Body:         payload,
	}

	log.Println("Publishing " + string(payload))
	err = c.Publish("", *queue, false, false, msg)
	if err != nil {
		log.Printf("basic.publish: %v", err)
		return
	}
}
