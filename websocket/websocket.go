package websocket

import (
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type SocketClient struct {
	Conn                *websocket.Conn
	url                 url.URL
	callbacks           callbacks
	autoReconnect       bool
	reconnectMaxRetries int
	reconnectMaxDelay   time.Duration
	connectTimeout      time.Duration
	reconnectAttempt    int
	SubscribeTokenList  []SubscribeTokenObj
	feedToken           string
	clientCode          string
	accessToken         string
	apiKey              string
}

type SmartStreamLtpTick struct {
	SubMode  uint64
	ExchType uint64
	Token    string
	SeqNo    uint64
	ExchTs   uint64
	Ltp      float32
}

type SmartStreamTick struct {
	SmartStreamLtpTick
}

type SmartStreamSubscribeRequestModel struct {
	CorrelationID string `json:"correlationID,omitempty"`
	Action        int    `json:"action"`
	Params        struct {
		Mode      int                 `json:"mode"`
		TokenList []SubscribeTokenObj `json:"tokenList"`
	} `json:"params"`
}

type SubscribeTokenObj struct {
	ExchangeType int      `json:"exchangeType"`
	Tokens       []string `json:"tokens"`
}

// callbacks represents callbacks available in ticker.
type callbacks struct {
	onMessage     func(tick SmartStreamTick)
	onNoReconnect func(int)
	onReconnect   func(int, time.Duration)
	onConnect     func()
	onClose       func(int, string)
	onError       func(error)
}

const (
	// Auto reconnect defaults
	// Default maximum number of reconnect attempts
	defaultReconnectMaxAttempts = 300
	// Auto reconnect min delay. Reconnect delay can't be less than this.
	reconnectMinDelay time.Duration = 5000 * time.Millisecond
	// Default auto reconnect delay to be used for auto reconnection.
	defaultReconnectMaxDelay time.Duration = 60000 * time.Millisecond
	// Connect timeout for initial server handshake.
	defaultConnectTimeout time.Duration = 7000 * time.Millisecond
	// Interval in which the connection check is performed periodically.
	connectionCheckInterval time.Duration = 10000 * time.Millisecond

	pingIntervalSeconds = 25
	pingStr             = ">>>ping>>>"
	pongStr             = "<<<pong<<<"

	actionSubscribe   = 0
	actionUnSubscribe = 1

	Nse_cm = 1
	Nse_fo = 2
	Bse_cm = 3
	Bse_fo = 4
	Mcx_fo = 5
	Ncx_fo = 7
	Cde_fo = 13

	ModeLTP = 1
	// only mode: LTP is currently supported
	// ModeQuote     = 2
	// ModeSnapQuote = 3
	// Mode20Depth   = 4
)

var (
	// Default ticker url.
	tickerURL = url.URL{Scheme: "wss", Host: "smartapisocket.angelone.in", Path: "smart-stream"}
)

// New creates a new ticker instance.
func New(clientCode, apiKey, feedToken, accessToken string) *SocketClient {
	sc := &SocketClient{
		clientCode:          clientCode,
		feedToken:           feedToken,
		accessToken:         accessToken,
		apiKey:              apiKey,
		url:                 tickerURL,
		autoReconnect:       true,
		reconnectMaxDelay:   defaultReconnectMaxDelay,
		reconnectMaxRetries: defaultReconnectMaxAttempts,
		connectTimeout:      defaultConnectTimeout,
	}

	return sc
}

// SetRootURL sets ticker root url.
func (s *SocketClient) SetRootURL(u url.URL) {
	s.url = u
}

// SetAccessToken set access token.
func (s *SocketClient) SetFeedToken(feedToken string) {
	s.feedToken = feedToken
}

// SetConnectTimeout sets default timeout for initial connect handshake
func (s *SocketClient) SetConnectTimeout(val time.Duration) {
	s.connectTimeout = val
}

// SetAutoReconnect enable/disable auto reconnect.
func (s *SocketClient) SetAutoReconnect(val bool) {
	s.autoReconnect = val
}

// SetReconnectMaxDelay sets maximum auto reconnect delay.
func (s *SocketClient) SetReconnectMaxDelay(val time.Duration) error {
	if val > reconnectMinDelay {
		return fmt.Errorf("ReconnectMaxDelay can't be less than %fms", reconnectMinDelay.Seconds()*1000)
	}

	s.reconnectMaxDelay = val
	return nil
}

// SetReconnectMaxRetries sets maximum reconnect attempts.
func (s *SocketClient) SetReconnectMaxRetries(val int) {
	s.reconnectMaxRetries = val
}

// OnConnect callback.
func (s *SocketClient) OnConnect(f func()) {
	s.callbacks.onConnect = f
}

// OnError callback.
func (s *SocketClient) OnError(f func(err error)) {
	s.callbacks.onError = f
}

// OnClose callback.
func (s *SocketClient) OnClose(f func(code int, reason string)) {
	s.callbacks.onClose = f
}

// OnMessage callback.
func (s *SocketClient) OnMessage(f func(message SmartStreamTick)) {
	s.callbacks.onMessage = f
}

// OnReconnect callback.
func (s *SocketClient) OnReconnect(f func(attempt int, delay time.Duration)) {
	s.callbacks.onReconnect = f
}

// OnNoReconnect callback.
func (s *SocketClient) OnNoReconnect(f func(attempt int)) {
	s.callbacks.onNoReconnect = f
}

// Serve starts the connection to ticker server. Since its blocking its recommended to use it in go routine.
func (s *SocketClient) Serve() {

	for {
		// If reconnect attempt exceeds max then close the loop
		if s.reconnectAttempt > s.reconnectMaxRetries {
			s.triggerNoReconnect(s.reconnectAttempt)
			return
		}
		// If its a reconnect then wait exponentially based on reconnect attempt
		if s.reconnectAttempt > 0 {
			nextDelay := time.Duration(math.Pow(2, float64(s.reconnectAttempt))) * time.Second
			if nextDelay > s.reconnectMaxDelay {
				nextDelay = s.reconnectMaxDelay
			}

			s.triggerReconnect(s.reconnectAttempt, nextDelay)

			// Close the previous connection if exists
			if s.Conn != nil {
				s.Conn.Close()
			}
		}
		// create a dialer
		d := websocket.DefaultDialer
		d.HandshakeTimeout = s.connectTimeout
		d.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
		headers := http.Header{
			"Authorization": []string{s.accessToken},
			"x-api-key":     []string{s.apiKey},
			"x-client-code": []string{s.clientCode},
			"x-feed-token":  []string{s.feedToken},
		}
		conn, _, err := d.Dial(s.url.String(), headers)
		if err != nil {
			if errors.Is(err, websocket.ErrBadHandshake) {
				log.Println(`wss handshake failed, please double check your credentials and account`)
				return
			}
			s.triggerError(err)
			// If auto reconnect is enabled then try reconnecting else return error
			if s.autoReconnect {
				s.reconnectAttempt++
				continue
			}
			return
		}

		// ping the server
		err = conn.WriteMessage(websocket.TextMessage, []byte(`ping`))
		if err != nil {
			s.triggerError(err)
			return
		}
		log.Println(pingStr)

		// server should respond pong
		_, message, err := conn.ReadMessage()
		if err != nil || string(message) != "pong" {
			s.triggerError(err)
			return
		}
		pongHandler()

		// schedule periodic pinger
		go func() {
			tk := time.NewTicker(time.Second * pingIntervalSeconds)
			defer tk.Stop()
			for {
				select {
				case <-tk.C:
					// ping the server
					err = conn.WriteMessage(websocket.TextMessage, []byte(`ping`))
					if err != nil {
						s.triggerError(err)
						return
					}
					log.Println(pingStr)
				}
			}

		}()

		// Close the connection when its done.
		defer s.Conn.Close()

		// Assign the current connection to the instance.
		s.Conn = conn

		// Trigger connect callback.
		s.triggerConnect()

		// Resubscribe to stored tokens
		if s.reconnectAttempt > 0 {
			_ = s.Resubscribe()
		}

		// Reset auto reconnect vars
		s.reconnectAttempt = 0

		// Set on close handler
		s.Conn.SetCloseHandler(s.handleClose)

		var wg sync.WaitGroup
		Restart := make(chan bool, 1)
		// Receive ticker data in a go routine.
		wg.Add(1)
		go s.readMessage(&wg, Restart)

		// Run watcher to check last ping time and reconnect if required
		if s.autoReconnect {
			wg.Add(1)
			go s.checkConnection(&wg, Restart)
		}

		// Wait for go routines to finish before doing next reconnect
		wg.Wait()
	}
}

func (s *SocketClient) handleClose(code int, reason string) error {
	s.triggerClose(code, reason)
	return nil
}

// Trigger callback methods
func (s *SocketClient) triggerError(err error) {
	if s.callbacks.onError != nil {
		s.callbacks.onError(err)
	}
}

func (s *SocketClient) triggerClose(code int, reason string) {
	if s.callbacks.onClose != nil {
		s.callbacks.onClose(code, reason)
	}
}

func (s *SocketClient) triggerConnect() {
	if s.callbacks.onConnect != nil {
		s.callbacks.onConnect()
	}
}

func (s *SocketClient) triggerReconnect(attempt int, delay time.Duration) {
	if s.callbacks.onReconnect != nil {
		s.callbacks.onReconnect(attempt, delay)
	}
}

func (s *SocketClient) triggerNoReconnect(attempt int) {
	if s.callbacks.onNoReconnect != nil {
		s.callbacks.onNoReconnect(attempt)
	}
}

func (s *SocketClient) triggerMessage(message SmartStreamTick) {
	if s.callbacks.onMessage != nil {
		s.callbacks.onMessage(message)
	}
}

// Periodically check for last ping time and initiate reconnect if applicable.
func (s *SocketClient) checkConnection(wg *sync.WaitGroup, Restart chan bool) {
	defer wg.Done()
	switch {
	case <-Restart:
		return
	}
}

// readMessage reads the data in a loop.
func (s *SocketClient) readMessage(wg *sync.WaitGroup, Restart chan bool) {
	defer wg.Done()
	for {
		msgType, msg, err := s.Conn.ReadMessage()
		if err != nil {
			s.triggerError(fmt.Errorf("Error reading data: %v", err))
			Restart <- true
			return
		}

		if msgType == websocket.TextMessage && string(msg) == "pong" {
			pongHandler()
			continue
		}

		// TODO: implement recovery from panic

		// only LTP mode is currently supported
		// assuming LTP tick, parse it
		ssTick := SmartStreamTick{}

		// ssTick.SubMode = binary.LittleEndian.Uint64(msg[0:1])
		ssTick.SubMode = uint64(msg[0])
		ssTick.ExchType = uint64(msg[1])
		ssTick.Token = string(msg[2:27])
		// sequence number not required for me, ignore it
		// ssTick.SeqNo = binary.LittleEndian.Uint64(msg[27:35])
		ssTick.ExchTs = binary.LittleEndian.Uint64(msg[35:43])
		ssTick.Ltp = float32(binary.LittleEndian.Uint32(msg[43:51])) / 100

		// Trigger message.
		s.triggerMessage(ssTick)

	}
}

// Close tries to close the connection gracefully. If the server doesn't close it
func (s *SocketClient) Close() error {
	return s.Conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
}

// Subscribe subscribes tick for the given list of tokens.
func (s *SocketClient) Subscribe(tokenList []SubscribeTokenObj) error {

	reqObj := SmartStreamSubscribeRequestModel{}
	reqObj.Action = actionUnSubscribe
	reqObj.Params.Mode = ModeLTP
	reqObj.Params.TokenList = tokenList

	err := s.Conn.WriteJSON(reqObj)
	if err != nil {
		s.triggerError(err)
		return err
	}

	s.SubscribeTokenList = tokenList
	return nil
}

func (s *SocketClient) Resubscribe() error {
	err := s.Subscribe(s.SubscribeTokenList)
	return err
}

// pongHandler is used to handle PongMessages for the Client
func pongHandler() {
	// TODO: update connectionCheck and reconnection handlers
	log.Println(pongStr)
}
