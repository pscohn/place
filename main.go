package main

import (
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/go-redis/redis"
	"github.com/gorilla/websocket"
)

var redisClient = connectRedis()
var clients = make(map[*websocket.Conn]bool)
var broadcast = make(chan Message)
var broadcastClients = make(chan ClientCountMessage)
var upgrader = websocket.Upgrader{}

// Message stuff
type Message struct {
	Type     string `json:"type"`
	Location string `json:"location"`
	Color    string `json:"color"`
}

// BoardMessage contains entire board state for initial load
type BoardMessage struct {
	Type  string        `json:"type"`
	Keys  []string      `json:"keys"`
	Board []interface{} `json:"board"`
}

// ClientCountMessage relays the number of connected users
type ClientCountMessage struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

func connectRedis() *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr:     os.Getenv("REDIS_URL"),
		Password: os.Getenv("REDIS_PASS"),
		DB:       0,
	})
	return client
}

func setColor(client *redis.Client, position string, color string) {
	err := client.Set(position, color, 0).Err()
	if err != nil {
		panic(err)
	}
}

func getColor(client *redis.Client, position string) string {
	val, err := client.Get(position).Result()
	if err != nil {
		panic(err)
	}
	return val
}

func initialKeys() []string {
	var keys []string
	for i := 0; i < 100; i++ {
		for j := 0; j < 100; j++ {
			keys = append(keys, strconv.Itoa(i)+"-"+strconv.Itoa(j))
		}
	}
	return keys
}

func getBoard(client *redis.Client) ([]string, []interface{}) {
	keys := initialKeys()
	ret := client.MGet(keys...)
	return keys, ret.Val()
}

func initializeBoard(client *redis.Client) {
	initialColor := "#ddd"
	keys := initialKeys()
	s := make([]interface{}, len(keys)*2)
	for i, v := range keys {
		idx := i * 2
		s[idx] = v
		s[idx+1] = initialColor
	}
	client.MSetNX(s...)
}

func closeClient(ws *websocket.Conn) {
	ws.Close()
	delete(clients, ws)
	goodbye := ClientCountMessage{"clients", len(clients)}
	broadcastClients <- goodbye
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer closeClient(ws)
	clients[ws] = true
	hello := ClientCountMessage{"clients", len(clients)}
	broadcastClients <- hello

	keys, board := getBoard(redisClient)
	err = ws.WriteJSON(BoardMessage{"board", keys, board})
	if err != nil {
		log.Printf("error: %v", err)
		ws.Close()
		delete(clients, ws)
	}

	for {
		var msg Message
		err := ws.ReadJSON(&msg)
		if err != nil {
			log.Printf("error: %v", err)
			delete(clients, ws)
			break
		}
		if msg.Type == "setColor" {
			broadcast <- msg
		}
	}
}

func handleMessages(redisClient *redis.Client) {
	for {
		msg := <-broadcast
		log.Print(msg)
		if msg.Type == "setColor" {
			setColor(redisClient, msg.Location, msg.Color)
		}
		for client := range clients {
			err := client.WriteJSON(msg)
			if err != nil {
				log.Printf("error: %v", err)
				client.Close()
				delete(clients, client)
			}
		}
	}
}

func handleClientMessages() {
	for {
		msg := <-broadcastClients
		log.Print(msg)
		for client := range clients {
			err := client.WriteJSON(msg)
			if err != nil {
				log.Printf("error: %v", err)
				client.Close()
				delete(clients, client)
			}
		}
	}
}

func main() {
	initializeBoard(redisClient)
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/", fs)
	http.HandleFunc("/ws", handleConnections)
	go handleMessages(redisClient)
	go handleClientMessages()
	port := os.Getenv("PORT")
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
