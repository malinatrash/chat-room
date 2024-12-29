package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sync"
)

type Room struct {
	ID        string
	Users     map[string]bool
	Messages  []Message
	UserCount int
	clients   map[chan Event]bool
	mu        sync.Mutex
}

type Message struct {
	Username string
	Content  string
}

type Event struct {
	Type    string      `json:"type"`
	Data    interface{} `json:"data"`
	Message string      `json:"message,omitempty"`
}

var (
	rooms = make(map[string]*Room)
	mu    sync.Mutex
)

func main() {
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/room/", handleRoom)
	http.HandleFunc("/join-room", handleJoinRoom)
	http.HandleFunc("/send-message", handleSendMessage)
	http.HandleFunc("/events/", handleEvents)
	http.HandleFunc("/leave-room", handleLeaveRoom)

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	fmt.Println("Server starting on :8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	tmpl := template.Must(template.ParseFiles("templates/home.html", "templates/notification.html"))
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

	tmpl := template.Must(template.ParseFiles("templates/room.html", "templates/notification.html"))
	tmpl.Execute(w, room)
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Path[len("/events/"):]
	username := r.URL.Query().Get("username")
	
	mu.Lock()
	room, exists := rooms[roomID]
	if !exists {
		mu.Unlock()
		http.Error(w, "Room not found", http.StatusNotFound)
		return
	}
	mu.Unlock()

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
			room.UserCount = len(room.Users)
			// Broadcast leave event
			event := Event{
				Type:    "leave",
				Message: fmt.Sprintf("%s left the room", username),
				Data: map[string]interface{}{
					"userCount": room.UserCount,
				},
			}
			for client := range room.clients {
				client <- event
			}
		}
		close(events)
		room.mu.Unlock()
	}()

	for {
		select {
		case event := <-events:
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
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
	
	room.mu.Lock()
	if _, exists := room.Users[username]; exists {
		room.mu.Unlock()
		mu.Unlock()
		http.Error(w, "Username already taken", http.StatusBadRequest)
		return
	}

	room.Users[username] = true
	room.UserCount = len(room.Users)

	event := Event{
		Type:    "join",
		Message: fmt.Sprintf("%s joined the room!", username),
		Data: map[string]interface{}{
			"userCount": room.UserCount,
		},
	}
	for client := range room.clients {
		client <- event
	}

	room.mu.Unlock()
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"type":      "success",
		"message":   fmt.Sprintf("%s joined the room!", username),
		"userCount": room.UserCount,
	})
}

func handleLeaveRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	roomID := r.FormValue("roomId")
	username := r.FormValue("username")

	mu.Lock()
	room, exists := rooms[roomID]
	if !exists {
		mu.Unlock()
		http.Error(w, "Room not found", http.StatusNotFound)
		return
	}

	room.mu.Lock()
	delete(room.Users, username)
	room.UserCount = len(room.Users)

	event := Event{
		Type:    "leave",
		Message: fmt.Sprintf("%s left the room", username),
		Data: map[string]interface{}{
			"userCount": room.UserCount,
		},
	}
	for client := range room.clients {
		client <- event
	}

	room.mu.Unlock()
	mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	roomID := r.FormValue("roomId")
	username := r.FormValue("username")
	content := r.FormValue("message")

	mu.Lock()
	room, exists := rooms[roomID]
	if !exists {
		mu.Unlock()
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
	mu.Unlock()

	w.WriteHeader(http.StatusOK)
}
