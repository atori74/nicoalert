package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var logger *log.Logger
var logFile *os.File

func initLogger() {
	logFilePath := "useragent.log"

	_, err := os.Stat(logFilePath)
	var err2 error
	if err != nil {
		logFile, err2 = os.Create(logFilePath)
	} else {
		logFile, err2 = os.OpenFile(logFilePath, os.O_RDWR|os.O_APPEND, 0666)
	}
	if err2 != nil {
		fmt.Println(err2.Error())
		logger = log.New(os.Stdout, "", log.LstdFlags)
		logger.Println("Cannot open the log file. Log to only stdout.")
	} else {
		logWriter := io.MultiWriter(os.Stdout, logFile)
		logger = log.New(logWriter, "", log.LstdFlags)
	}
}

func main() {
	initLogger()
	defer logFile.Close()

	logger.Println("User Agent started.")

	// websocket
	url := url.URL{Scheme: "wss", Host: "push.services.mozilla.com", Path: "/"}
	header := http.Header{}
	header.Set("Sec-WebSocket-Protocol", "push-notification")
	header.Set("Ssc-WebSocket-Version", "13")
	conn, _, err := websocket.DefaultDialer.Dial(url.String(), header)
	handleError(err)
	defer conn.Close()

	finish := make(chan struct{})
	go func() {
		defer close(finish)
		for {
			_, readMsg, err := conn.ReadMessage()
			handleError(err)
			logger.Println(string(readMsg))
		}
	}()

	UAID := "d55e73cfff10472d951d99ee410eaa2b"

	helloMsg := fmt.Sprintf("{\"messageType\":\"hello\",\"broadcasts\":null,\"use_webpush\":true,\"uaid\":\"%s\"}", UAID)
	conn.WriteMessage(websocket.TextMessage, []byte(helloMsg))
	// {"messageType":"hello","uaid":"d55e73cfff10472d951d99ee410eaa2b","status":200,"use_webpush":true,"broadcasts":{}}

	chid := uuid.New().String()
	vapidBase64 := "BC08Fdr2JChSL0kr5imO99L6zZG6Rn0tBAWNTlrZfJtsDoeAvmJSa7CnUOHpNhd5zOk0YnRToEOT47YLet8Dpig="
	registerMsg := fmt.Sprintf("{\"channelID\":\"%s\",\"messageType\":\"register\",\"key\":\"%s\"}", chid, vapidBase64)
	conn.WriteMessage(websocket.TextMessage, []byte(registerMsg))
	// {"messageType":"register","channelID":"cb0d5497-b58d-4291-b9e3-3c2df5dcde7e","status":200,"pushEndpoint":"https://updates.push.services.mozilla.com/wpush/v2/gAAAAABlRhweIud02DivV5g9dCZaxjwR1dMXQyrxJ-_EmcFmLWCJ_FWjupSZI9lOBTL26npLeUtVooYMHkx-AV224mckfpMPCkAm9e8OHnEqqG4iksdeK2aHLWsC0Whzzlf7NB7YlZ1Pd328cOacekQUWsmrork0j5zIBRkrovfkDM2CTAfoPsk"}

	pushEndpoint := "https://updates.push.services.mozilla.com/wpush/v2/gAAAAABlRhweIud02DivV5g9dCZaxjwR1dMXQyrxJ-_EmcFmLWCJ_FWjupSZI9lOBTL26npLeUtVooYMHkx-AV224mckfpMPCkAm9e8OHnEqqG4iksdeK2aHLWsC0Whzzlf7NB7YlZ1Pd328cOacekQUWsmrork0j5zIBRkrovfkDM2CTAfoPsk"

	// register push endpoint to niconico application server
	ecdhPrikey, err := ecdh.P256().GenerateKey(rand.Reader)
	handleError(err)
	ecdhPubkey := ecdhPrikey.PublicKey()
	logger.Printf("ECDH Private Key: %s\n", base64.StdEncoding.EncodeToString(ecdhPrikey.Bytes()))
	logger.Printf("ECDH Public Key: %s\n", base64.StdEncoding.EncodeToString(ecdhPubkey.Bytes()))

	auth := make([]byte, 16)
	_, err = io.ReadFull(rand.Reader, auth)
	handleError(err)
	logger.Printf("Auth Secret: %s\n", base64.StdEncoding.EncodeToString(auth))

	// prepare http client
	if true {
		jar, err := niconicoLogin()
		handleError(err)

		client := http.Client{Jar: jar}

		// Old: registerEndpoint := "https://public.api.nicovideo.jp/v1/nicopush/webpush/endpoints.json"
		registerEndpoint := "https://api.push.nicovideo.jp/v1/nicopush/webpush/endpoints.json"
		registerParams := fmt.Sprintf(
			"{\"destApp\":\"nico_account_webpush\",\"endpoint\":{\"endpoint\":\"%s\",\"auth\":\"%s\",\"p256dh\":\"%s\"}}",
			pushEndpoint,
			base64.StdEncoding.EncodeToString(auth),
			base64.StdEncoding.EncodeToString(ecdhPubkey.Bytes()),
		)

		req, err := http.NewRequest("POST", registerEndpoint, strings.NewReader(registerParams))
		req.Header.Set("Referer", "https://account.nicovideo.jp/my/account")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-request-with", "https://account.nicovideo.jp/my/account")
		req.Header.Set("x-frontend-id", "8")

		res, err := client.Do(req)
		handleError(err)

		logger.Printf("Register endpoint to server: %s\n", res.Status)
	}

	<-finish
}

func niconicoLogin() (http.CookieJar, error) {
	niconicoEMail := os.Getenv("NICONICO_EMAIL")
	niconicoPassword := os.Getenv("NICONICO_PASSWORD")
	if niconicoEMail == "" || niconicoPassword == "" {
		return nil, errors.New("Necessary environment variables for niconico login are not set.")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Jar: jar}
	loginURL := "https://account.nicovideo.jp/login/redirector?site=niconico"
	params := url.Values{}
	params.Set("mail_tel", niconicoEMail)
	params.Set("password", niconicoPassword)
	payload := strings.NewReader(params.Encode())
	req, err := http.NewRequest("POST", loginURL, payload)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	_, err = client.Do(req)
	if err != nil {
		return nil, err
	}
	return jar, nil
}

func handleError(e error) {
	if e != nil {
		logger.Println(e.Error())
		os.Exit(1)
	}
}
