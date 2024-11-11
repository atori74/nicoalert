package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/hkdf"
)

const (
	PUSH_SERVICE               = "push.services.mozilla.com"
	NICONICO_VAPID             = "BC08Fdr2JChSL0kr5imO99L6zZG6Rn0tBAWNTlrZfJtsDoeAvmJSa7CnUOHpNhd5zOk0YnRToEOT47YLet8Dpig="
	NICONICO_REGISTER_ENDPOINT = "https://api.push.nicovideo.jp/v1/nicopush/webpush/endpoints.json"
	IS_DEBUG                   = false
)

type UserAgent struct {
	PushServiceHost string
	UAID            string
	Subscriptions   []*Subscription
}

func (ua *UserAgent) connectPushService() (*websocket.Conn, error) {
	retry := 5
	var err error
	for i := 0; i < retry+1; i++ {
		url := url.URL{Scheme: "wss", Host: ua.PushServiceHost, Path: "/"}
		header := http.Header{}
		header.Set("Sec-WebSocket-Protocol", "push-notification")
		header.Set("Ssc-WebSocket-Version", "13")
		conn, _, err := websocket.DefaultDialer.Dial(url.String(), header)
		if err != nil {
			continue
		}

		hello := struct {
			MessageType string       `json:"messageType"`
			Broadcasts  *interface{} `json:"broadcasts"`
			UseWebpush  bool         `json:"use_webpush"`
			UAID        string       `json:"uaid,omitempty"`
		}{
			MessageType: "hello",
			Broadcasts:  nil,
			UseWebpush:  true,
			UAID:        ua.UAID,
		}

		m, err := json.Marshal(hello)
		if err != nil {
			conn.Close()
			continue
		}
		err = conn.WriteMessage(websocket.TextMessage, m)
		if err != nil {
			conn.Close()
			continue
		}

		_, r, err := conn.ReadMessage()
		if err != nil {
			conn.Close()
			continue
		}
		var rec map[string]interface{}
		err = json.Unmarshal(r, &rec)
		if err != nil {
			conn.Close()
			continue
		}
		if rec["messageType"].(string) != "hello" {
			err = errors.New("Did not receive hello response")
			continue
		}
		ua.UAID = rec["uaid"].(string)

		return conn, nil
	}
	return nil, err
}

// Subscriptionをregisterする
// Input: chid, VapID
// Output: PushEndpoint
func (ua *UserAgent) registerSubscription(conn *websocket.Conn, vapID string) (*Subscription, error) {
	chID := uuid.New().String()
	reg := struct {
		ChannelID   string `json:"channelID"`
		MessageType string `json:"messageType"`
		Key         string `json:"key"`
	}{
		ChannelID:   chID,
		MessageType: "register",
		Key:         vapID,
	}
	m, err := json.Marshal(reg)
	if err != nil {
		return nil, err
	}
	conn.WriteMessage(websocket.TextMessage, m)

	_, r, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	var rec map[string]interface{}
	err = json.Unmarshal(r, &rec)
	if err != nil {
		return nil, err
	}
	if !(rec["messageType"].(string) == "register" && rec["status"].(float64) == 200) {
		return nil, errors.New(fmt.Sprintf("Failed to register: %s", string(r)))
	}
	pushEndpoint := rec["pushEndpoint"].(string)

	auth := make([]byte, 16)
	_, err = io.ReadFull(rand.Reader, auth)
	if err != nil {
		return nil, err
	}

	privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	s := &Subscription{
		ChID:          chID,
		VapID:         vapID,
		PushEndpoint:  pushEndpoint,
		Auth:          auth,
		ClientPrivate: privateKey,
	}
	ua.Subscriptions = append(ua.Subscriptions, s)

	return s, nil
}

func (ua *UserAgent) ReadMessages(conn *websocket.Conn) error {
	Debug("Waiting for messages from push service.")
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			conn.Close()
			var err2 error
			conn, err2 = ua.connectPushService()
			if err2 != nil {
				return err2
			}
			continue
		}

		Debug(string(msg))

		var m map[string]interface{}
		json.Unmarshal(msg, &m)
		if t, ok := m["messageType"]; ok && t.(string) == "notification" {
			for _, s := range ua.Subscriptions {
				if s.ChID == m["channelID"].(string) {
					data, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(m["data"].(string))
					if err != nil {
						break
					}
					dec, err := s.DecryptMessage(data)
					if err != nil {
						break
					}
					Debug(fmt.Sprintf("Decrypted bytes: %v", dec))
					fmt.Printf("%s\n", dec)
					break
				}
			}
		}
	}
}

type Subscription struct {
	ChID          string
	VapID         string
	PushEndpoint  string
	Auth          []byte
	ClientPrivate *ecdh.PrivateKey
}

func (s *Subscription) requestPushDelivery(requestURI string) error {
	jar, err := niconicoLogin()
	if err != nil {
		Debug("Error in niconico login.")
		return err
	}

	client := http.Client{Jar: jar}

	params := fmt.Sprintf(
		`{"destApp":"nico_account_webpush","endpoint":{"endpoint":"%s","auth":"%s","p256dh":"%s"}}`,
		s.PushEndpoint,
		base64.StdEncoding.EncodeToString(s.Auth),
		base64.StdEncoding.EncodeToString(s.ClientPrivate.PublicKey().Bytes()),
	)

	req, err := http.NewRequest("POST", requestURI, strings.NewReader(params))
	req.Header.Set("Referer", "https://account.nicovideo.jp/")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-request-with", "https://account.nicovideo.jp/my/account")
	req.Header.Set("x-frontend-id", "8")

	res, err := client.Do(req)
	if err != nil {
		return err
	} else if res.StatusCode != 200 {
		body, _ := io.ReadAll(res.Body)
		Debug(fmt.Sprintf("%s: %s", res.Status, string(body)))
		return errors.New("Error occured in push request to niconico.")
	}
	return nil
}

func (s *Subscription) DecryptMessage(payload []byte) ([]byte, error) {
	// ECDH
	asPublicBytes, err := getAsPublicKey(payload)
	if err != nil {
		return nil, err
	}
	asPublic, err := ecdh.P256().NewPublicKey(asPublicBytes)
	if err != nil {
		return nil, err
	}
	sharedSecret, err := s.ClientPrivate.ECDH(asPublic)
	if err != nil {
		return nil, err
	}

	salt := payload[0:16]
	cipherText := payload[21+len(asPublicBytes):]

	// derive key
	prkInfoBuf := bytes.NewBuffer([]byte("WebPush: info\x00"))
	prkInfoBuf.Write(s.ClientPrivate.PublicKey().Bytes())
	prkInfoBuf.Write(asPublic.Bytes())

	hash := sha256.New
	prkHKDF := hkdf.New(hash, sharedSecret, s.Auth, prkInfoBuf.Bytes())
	ikm, err := getHKDFKey(prkHKDF, 32)
	if err != nil {
		return nil, err
	}

	contentEncryptionKeyInfo := []byte("Content-Encoding: aes128gcm\x00")
	contentHKDF := hkdf.New(hash, ikm, salt, contentEncryptionKeyInfo)
	contentEncryptionKey, err := getHKDFKey(contentHKDF, 16)
	if err != nil {
		return nil, err
	}

	nonceInfo := []byte("Content-Encoding: nonce\x00")
	nonceHKDF := hkdf.New(hash, ikm, salt, nonceInfo)
	nonce, err := getHKDFKey(nonceHKDF, 12)
	if err != nil {
		return nil, err
	}

	// AES128GCM decrypt
	c, err := aes.NewCipher(contentEncryptionKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(c)
	if err != nil {
		return nil, err
	}

	plainText, err := gcm.Open(nil, nonce, cipherText, nil)
	if err != nil {
		return plainText, err
	}

	// Remove padding
	message := removePadding(plainText, 0x02)

	return message, nil
}

func removePadding(payload []byte, sep byte) []byte {
	li := bytes.LastIndexByte(payload, sep)
	if li == -1 {
		return payload
	}
	return payload[:li]
}

func getHKDFKey(hkdf io.Reader, length int) ([]byte, error) {
	key := make([]byte, length)
	n, err := io.ReadFull(hkdf, key)
	if n != len(key) || err != nil {
		return key, err
	}

	return key, nil
}

func getAsPublicKey(payload []byte) ([]byte, error) {
	if len(payload) < 86 {
		return nil, errors.New("Payload length is too short.")
	}
	publicKeyLength := int(payload[20])
	publicKey := payload[21 : 21+publicKeyLength]
	return publicKey, nil
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

func main() {
	userAgent := UserAgent{
		PushServiceHost: PUSH_SERVICE,
		UAID:            "",
		Subscriptions:   []*Subscription{},
	}

	// Websocketコネクションを張る
	// Output: conn
	// helloを送る
	// Output: uaid
	conn, err := userAgent.connectPushService()
	if err != nil {
		Debug(err)
		return
	}
	Debug("Connected to push service successfully.")

	// Subscriptionをregisterする
	// Input: chid, VapID
	// Output: PushEndpoint
	subscription, err := userAgent.registerSubscription(conn, NICONICO_VAPID)
	if err != nil {
		Debug(err)
		return
	}
	Debug("Registered subscription successfully.")

	// ニコニコAPにPushEndpointを登録する
	// Input: PushEndpoint, Authシークレット, クライアントPubkey
	// Output: ステータスコード
	err = subscription.requestPushDelivery(NICONICO_REGISTER_ENDPOINT)
	if err != nil {
		Debug(err)
		return
	}
	Debug("Requested for push delivery to niconico.")

	// WebsocketからNotificationを受け取る
	// Input: None
	// Output: メッセージ
	err = userAgent.ReadMessages(conn)
	if err != nil {
		Debug(err)
		return
	}
}

func Debug(v any) {
	if IS_DEBUG {
		fmt.Fprintln(os.Stderr, v)
	}
}
