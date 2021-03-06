package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"

	cricd "github.com/cricd/cricd-go"

	log "github.com/Sirupsen/logrus"
	es "github.com/cricd/es"
	"github.com/gorilla/mux"
)

type cricdEventConfig struct {
	nextBallURL  string
	nextBallPort string
}

var config cricdEventConfig
var client es.CricdESClient

func init() {
	log.SetLevel(log.InfoLevel)
	log.SetOutput(os.Stdout)
}

func (config *cricdEventConfig) useDefault() {
	nbURL := os.Getenv("NEXT_BALL_IP")
	if nbURL != "" {
		config.nextBallURL = nbURL
	} else {
		log.WithFields(log.Fields{"value": "NEXT_BALL_IP"}).Info("Unable to find env var, using default `localhost`")
		config.nextBallURL = "localhost"
	}

	nbPort := os.Getenv("NEXT_BALL_PORT")
	if nbPort != "" {
		config.nextBallPort = nbPort
	} else {
		log.WithFields(log.Fields{"value": "NEXT_BALL_PORT"}).Info("Unable to find env var, using default `3004`")
		config.nextBallPort = "3004"
	}
}

func getNextEvent(config *cricdEventConfig, event cricd.Delivery) (string, error) {

	url := "http://" + config.nextBallURL + ":" + config.nextBallPort
	resp, err := http.Get(url + "?match=" + strconv.Itoa(event.MatchID))
	if err != nil {
		log.WithFields(log.Fields{"value": err}).Errorf("Unable to get next event")
		return "", err
	}
	nextEvent, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.WithFields(log.Fields{"value": err}).Errorf("Unable to parse next event")
		return "", err
	}
	return string(nextEvent), nil
}

func main() {
	config.useDefault()
	client.UseDefaultConfig()
	ok := client.Connect()
	if !ok {
		log.Panicln("Unable to connect to EventStore")
	}
	router := mux.NewRouter().StrictSlash(true)
	router.HandleFunc("/event", eventHandler)
	http.ListenAndServe(":4567", router)

}

func eventHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers",
		"Accept, Content-Type, Content-Length, Accept-Encoding, Authorization")

	switch r.Method {
	case "OPTIONS":
		w.WriteHeader(200)
		return
	case "POST":
		event, err := ioutil.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(400)
			fmt.Fprintf(w, "Unable to read event")
			log.WithFields(log.Fields{"value": err}).Errorf("Unable to read event from request")
			return
		}
		var cd cricd.Delivery
		err = json.Unmarshal(event, &cd)
		if err != nil {
			w.WriteHeader(500)
			log.WithFields(log.Fields{"value": err}).Errorf("Failed to unmarshal event to a cricd Delivery")
			fmt.Fprintf(w, "Failed to unmarshal event %v", err)
			return
		}

		// Validate the delivery
		ok, err := cd.Validate()
		if err != nil {
			w.WriteHeader(400)
			log.WithFields(log.Fields{"value": err}).Errorf("Failed to validate delivery with error")
			fmt.Fprintf(w, "Invalid event passed - %s", err)
			return

		} else if !ok {
			w.WriteHeader(400)
			log.Error("Failed to validate delivery without error")
			fmt.Fprintf(w, "Invalid delivery received")
			return
		}

		uuid, err := client.PushEvent(cd, false)
		if err != nil {
			w.WriteHeader(500)
			log.WithFields(log.Fields{"value": err}).Errorf("Failed to push event to ES")
			fmt.Fprintf(w, "Failed to push event %v", err)
			return
		}
		if uuid == "" {
			w.WriteHeader(500)
			log.Errorf("Failed to push event without error")
			fmt.Fprintf(w, "Internal server error")
			return
		}

		params := r.URL.Query()
		if params.Get("nextEvent") != "false" {
			log.Info("Getting next event for game")
			nextEvent, err := getNextEvent(&config, cd)
			if err != nil {
				w.WriteHeader(500)
				log.Errorf("Error when getting next event - %v", err)
				fmt.Fprintf(w, "Error from next ball processor - %v", err)
				return
			}
			if nextEvent != "" {
				w.WriteHeader(201)
				fmt.Fprintf(w, nextEvent)
				return
			}

		}
		w.WriteHeader(201)
		log.WithFields(log.Fields{"value": uuid}).Info("Successfully pushed event to ES")
		return
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
}
