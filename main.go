package main

import (
	"droneRtspClient/RTSPClient"
	"log"
	"os"
	"os/signal"
	"syscall"
)

var logger = log.New(os.Stdout, "[APP] ", log.LstdFlags)

func main() {
	rtspStream, err := RTSPClient.NewRTSPClient("192.168.201.1", "7070", "H264VideoSMS", logger)
	if err != nil {
		logger.Fatalf("error establishing RTSP connection %s", err)
	}

	_, err = rtspStream.Options()
	if err != nil {
		logger.Fatalf("error getting options: %s", err)
	}
	_, err = rtspStream.Describe()
	if err != nil {
		logger.Fatalf("error getting description: %s", err)
	}
	_, err = rtspStream.Setup()
	if err != nil {
		logger.Fatalf("error setting up: %s", err)
	}
	_, err = rtspStream.Play()
	if err != nil {
		logger.Fatalf("error when initiating play: %s", err)
	}

	// Setup signal handling for graceful shutdown
	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigs
		logger.Printf("Received signal: %s, shutting down...", sig)
		if err := rtspStream.Close(); err != nil {
			logger.Printf("error closing rtspStream: %s", err)
		}
		done <- true
	}()

	// Start reading RTP packets
	go rtspStream.ReadRtpPacketAndStreamToFFmpeg()

	// Wait until signal caught
	<-done
	logger.Println("Shutdown complete, exiting.")
}
