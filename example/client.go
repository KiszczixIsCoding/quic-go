package main

import (
	"crypto/tls"
	"fmt"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"io"
	"net/http"
	"time"
)

func main() {

	tr := &http3.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{http3.NextProtoH3},
		},
		QUICConfig: &quic.Config{
			KeepAlivePeriod: 2 * time.Second,
		},
	}
	defer tr.Close()

	client := &http.Client{
		Transport: tr,
		Timeout:   5 * time.Second,
	}

	fmt.Println("Wysyłam request...")

	resp, err := client.Get("https://localhost:4443")
	if err != nil {
		fmt.Println("Błąd:", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Println("Status:", resp.Status)
	fmt.Println("Body:", string(body))
}
