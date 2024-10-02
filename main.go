// chatapp-service-backend/main.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
)

type ChatApp struct {
	pubsubURL string
}

func NewChatApp(pubsubURL string) *ChatApp {
	return &ChatApp{
		pubsubURL: pubsubURL,
	}
}

// Middleware to handle CORS
func (ca *ChatApp) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow all origins for simplicity; adjust as needed
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		// Handle preflight OPTIONS request
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Handler to send a message
func (ca *ChatApp) sendHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var req struct {
		Topic   string `json:"topic"`
		Message string `json:"message"`
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Cannot read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	err = json.Unmarshal(body, &req)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Publish the message to pubsub-service
	pubURL := fmt.Sprintf("%s/publish", ca.pubsubURL)
	pubReqBody, _ := json.Marshal(map[string]string{
		"topic":   req.Topic,
		"message": req.Message,
	})

	resp, err := http.Post(pubURL, "application/json", bytes.NewBuffer(pubReqBody))
	if err != nil {
		http.Error(w, "Failed to publish message", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Failed to publish message", resp.StatusCode)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Message sent successfully"))
}

// Handler to receive messages using Server-Sent Events (SSE)
func (ca *ChatApp) receiveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	topic := r.URL.Query().Get("topic")
	if topic == "" {
		http.Error(w, "Missing topic", http.StatusBadRequest)
		return
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Subscribe to the topic from pubsub-service
	subURL := fmt.Sprintf("%s/subscribe?topic=%s", ca.pubsubURL, topic)
	resp, err := http.Get(subURL)
	if err != nil {
		http.Error(w, "Failed to subscribe to topic", http.StatusInternalServerError)
		return
	}

	// Ensure connection is closed when client disconnects
	notify := w.(http.CloseNotifier).CloseNotify()
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		<-notify
		resp.Body.Close()
		wg.Done()
	}()

	// Stream messages to the client
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			message := string(buf[:n])
			// Format message as SSE
			fmt.Fprintf(w, "data: %s\n\n", message)
			// Flush the response
			flusher, ok := w.(http.Flusher)
			if ok {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}

	wg.Wait()
}

func main() {
	pubsubURL := "http://localhost:8080" // Adjust if pubsub-service is on a different host/port
	chatApp := NewChatApp(pubsubURL)

	// fs := http.FileServer(http.Dir("../chatapp-ui-frontend"))
	// http.Handle("/", fs)

	mux := http.NewServeMux()
	mux.HandleFunc("/send", chatApp.sendHandler)
	mux.HandleFunc("/receive", chatApp.receiveHandler)
	mux.HandleFunc("/index", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../chatapp-ui-frontend/index.html")
	})

	// Wrap the mux with CORS middleware
	handler := chatApp.corsMiddleware(mux)

	log.Println("ChatApp service running on :8081")
	log.Fatal(http.ListenAndServe(":8081", handler))
}
