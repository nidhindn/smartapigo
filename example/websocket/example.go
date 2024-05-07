package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/pquerna/otp/totp"

	SmartApi "github.com/nidhindn/smartapigo"
	"github.com/nidhindn/smartapigo/websocket"
)

// ABClientConfig is the struct to store client configs and credentials
type ABClientConfig struct {
	ClientCode string
	Mpin       string
	APIKey     string
	TOPTSecret string
}

var socketClient *websocket.SocketClient

// Triggered when any error is raised
func onError(err error) {
	fmt.Println("Error: ", err)
}

// Triggered when websocket connection is closed
func onClose(code int, reason string) {
	fmt.Println("Close: ", code, reason)
}

// Triggered when connection is established and ready to send and accept data
func onConnect() {
	log.Println("subscribing...")
	// banknifty, nifty  "99926009", "99926000"

	var scripes []websocket.SubscribeTokenObj
	scripes = append(scripes,
		websocket.SubscribeTokenObj{
			ExchangeType: websocket.Nse_cm,
			Tokens:       []string{"99926009xxx"},
		},
		websocket.SubscribeTokenObj{
			ExchangeType: websocket.Nse_fo,
			Tokens:       []string{"43677", "43683xxx"},
		})
	err := socketClient.Subscribe(scripes)
	if err != nil {
		fmt.Println("err: ", err)
	}
	log.Println("subscribed...")
}

// Triggered when a message is received
func onMessage(message websocket.SmartStreamTick) {
	fmt.Printf("Message Received :- %v\n", message)
}

// Triggered when reconnection is attempted which is enabled by default
func onReconnect(attempt int, delay time.Duration) {
	fmt.Printf("Reconnect attempt %d in %fs\n", attempt, delay.Seconds())
}

// Triggered when maximum number of reconnect attempt is made and the program is terminated
func onNoReconnect(attempt int) {
	fmt.Printf("Maximum no of reconnect attempt reached: %d\n", attempt)
}

func mustReadClientConfig() (ABClientConfig, error) {
	return ABClientConfig{
		ClientCode: os.Getenv("CLIENT_CODE"),
		Mpin:       os.Getenv("MPIN"),
		APIKey:     os.Getenv("API_KEY"),
		TOPTSecret: os.Getenv("TOPT_SECRET"),
	}, nil
}

func main() {

	// Read client config
	// cc -> clientConfig
	cc, _ := mustReadClientConfig()

	// Create New Angel Broking Client
	ABClient := SmartApi.New(cc.ClientCode, cc.Mpin, cc.APIKey)

	// generate tOtp from tOtp secret stored in TOTP_SECRET env
	tOtp, err := totp.GenerateCode(cc.TOPTSecret, time.Now())

	if err != nil {
		log.Fatal("tOtp generation failed, error:", err)
	}

	// User Login and Generate User Session
	session, err := ABClient.GenerateSession(tOtp)

	if err != nil {
		fmt.Println(err)
		return
	}

	// Get User Profile
	session.UserProfile, err = ABClient.GetUserProfile()

	if err != nil {
		fmt.Println(err)
		return
	}

	// New Websocket Client
	socketClient = websocket.New(session.ClientCode, cc.APIKey, session.FeedToken, session.AccessToken)

	// Assign callbacks
	socketClient.OnError(onError)
	socketClient.OnClose(onClose)
	socketClient.OnMessage(onMessage)
	socketClient.OnConnect(onConnect)
	socketClient.OnReconnect(onReconnect)
	socketClient.OnNoReconnect(onNoReconnect)

	// Start Consuming Data
	socketClient.Serve()

}
