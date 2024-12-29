package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sync"
)

type Message struct {
	Username string
	Content  string
}

type Room struct {
	ID        string
	Messages  []Message
	Users     map[string]bool
	UserCount int
	clients   map[chan Event]bool
	mu        sync.RWMutex
}

type Event struct {
	Type    string      `json:"type"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

var (
	rooms = make(map[string]*Room)
	mu    sync.RWMutex
)

func main() {
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/room/", handleRoom)
	http.HandleFunc("/join-room", handleJoinRoom)
	http.HandleFunc("/leave-room", handleLeaveRoom)
	http.HandleFunc("/send-message", handleMessage)
	http.HandleFunc("/events/", handleEvents)

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/home.html"))
	tmpl.Execute(w, nil)
}

func handleRoom(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Path[len("/room/"):]

	mu.Lock()
	room, exists := rooms[roomID]
	if !exists {
		room = &Room{
			ID:      roomID,
			Users:   make(map[string]bool),
			clients: make(map[chan Event]bool),
		}
		rooms[roomID] = room
	}
	mu.Unlock()

	room.mu.RLock()
	data := struct {
		ID        string
		Messages  []Message
		UserCount int
	}{
		ID:        room.ID,
		Messages:  room.Messages,
		UserCount: len(room.Users),
	}
	room.mu.RUnlock()

	tmpl := template.Must(template.ParseFiles("templates/room.html"))
	tmpl.Execute(w, data)
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Path[len("/events/"):]
	username := r.URL.Query().Get("username")

	mu.RLock()
	room, exists := rooms[roomID]
	mu.RUnlock()

	if !exists {
		http.Error(w, "Room not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	events := make(chan Event)
	room.mu.Lock()
	room.clients[events] = true
	room.mu.Unlock()

	defer func() {
		room.mu.Lock()
		delete(room.clients, events)
		if username != "" {
			delete(room.Users, username)
			userCount := len(room.Users)
			room.UserCount = userCount
			event := Event{
				Type:    "leave",
				Message: fmt.Sprintf("%s left the room", username),
				Data: map[string]interface{}{
					"userCount": userCount,
					"username": username,
				},
			}
			for client := range room.clients {
				client <- event
			}
		}
		room.mu.Unlock()
		close(events)
	}()

	notify := w.(http.CloseNotifier).CloseNotify()
	go func() {
		<-notify
		room.mu.Lock()
		delete(room.clients, events)
		room.mu.Unlock()
	}()

	for {
		select {
		case event := <-events:
			data, err := json.Marshal(event)
			if err != nil {
				log.Printf("Error marshalling event: %v", err)
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			w.(http.Flusher).Flush()
		case <-notify:
			return
		}
	}
}

func handleJoinRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	roomID := r.FormValue("roomId")
	username := r.FormValue("username")

	mu.Lock()
	room, exists := rooms[roomID]
	if !exists {
		room = &Room{
			ID:      roomID,
			Users:   make(map[string]bool),
			clients: make(map[chan Event]bool),
		}
		rooms[roomID] = room
	}
	mu.Unlock()

	room.mu.Lock()
	if _, exists := room.Users[username]; exists {
		room.mu.Unlock()
		http.Error(w, "Username already taken", http.StatusBadRequest)
		return
	}

	room.Users[username] = true
	userCount := len(room.Users)
	room.UserCount = userCount

	event := Event{
		Type:    "join",
		Message: fmt.Sprintf("%s joined the room!", username),
		Data: map[string]interface{}{
			"userCount": userCount,
			"username": username,
		},
	}
	for client := range room.clients {
		client <- event
	}
	room.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"userCount": userCount,
	})
}

func handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	roomID := r.FormValue("roomId")
	username := r.FormValue("username")
	content := r.FormValue("message")

	mu.RLock()
	room, exists := rooms[roomID]
	mu.RUnlock()

	if !exists {
		http.Error(w, "Room not found", http.StatusNotFound)
		return
	}

	message := Message{
		Username: username,
		Content:  content,
	}

	room.mu.Lock()
	room.Messages = append(room.Messages, message)
	event := Event{
		Type: "message",
		Data: message,
	}
	for client := range room.clients {
		client <- event
	}
	room.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func handleLeaveRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	roomID := r.FormValue("roomId")
	username := r.FormValue("username")

	mu.RLock()
	room, exists := rooms[roomID]
	mu.RUnlock()

	if !exists {
		http.Error(w, "Room not found", http.StatusNotFound)
		return
	}

	room.mu.Lock()
	delete(room.Users, username)
	userCount := len(room.Users)
	room.UserCount = userCount

	event := Event{
		Type:    "leave",
		Message: fmt.Sprintf("%s left the room", username),
		Data: map[string]interface{}{
			"userCount": userCount,
			"username": username,
		},
	}
	for client := range room.clients {
		client <- event
	}
	room.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}
