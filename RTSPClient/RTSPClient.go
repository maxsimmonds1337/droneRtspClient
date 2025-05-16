package RTSPClient

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Info struct {
	Conn         net.Conn // holds the tcp connection
	Addr         string
	Url          string
	Timeout      time.Duration
	Session      string
	PreviousResp *RtspResponse
	CSeq         int
	PreviousCmd  string
	ffmpegCmd    *exec.Cmd
	ffmpegIn     io.WriteCloser
	Logger       *log.Logger
	currentFU    []byte
	sps          []byte
	pps          []byte
}

type RtspResponse struct {
	StatusLine string
	Headers    map[string]string
	Body       string // TODO: maybe have a dedicated struct for this - SdpResponse
}

// NewRTSPClient generates a new client. `host` is the host address, IE
// 192.168.0.1, `port` is the port address, for example 7070, and path is the
// RTSP streaming URL, like H264VideoSMS
// TODO: This only work with ipv4 address, not ipv6
func NewRTSPClient(host string, port string, path string, logger *log.Logger) (*Info, error) {
	urlAndPort := fmt.Sprintf("%s:%s", host, port)
	conn, err := net.Dial("tcp", urlAndPort)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("rtsp://%s:%s/%s", host, port, path)

	return &Info{
		Conn:         conn,
		Addr:         urlAndPort,
		Timeout:      0,
		Url:          url,
		CSeq:         1,
		PreviousCmd:  "",
		PreviousResp: &RtspResponse{},
		Logger:       logger,
	}, nil
}

// sendRtspCmd is used to send an RTSP command, for example, `OPTIONS` to an
// RTSP server.
func (c *Info) sendRtspCmd(cmd string, headers map[string]string) error {
	var sb strings.Builder

	//TODO: remove the hardcoded track, needs better unpacking SDP packet
	header := fmt.Sprintf("%s %s RTSP/1.0\r\nCSeq: %d\r\n", cmd, c.Url, c.CSeq)
	sb.Write([]byte(header))
	if c.Session != "" {
		sessionHeader := fmt.Sprintf("Session: %s\r\n", c.Session)
		sb.Write([]byte(sessionHeader))
	}

	for k, v := range headers {
		sb.Write([]byte(k + ": " + v + "\r\n"))
	}

	sb.Write([]byte("\r\n"))
	req := sb.String()

	c.Logger.Printf("Sending request:\r\n%s", req)
	// I suspect there's an issue with this because server time is wrong
	// and so this fails straight away
	// if c.Timeout != 0 {
	// 	c.Conn.SetDeadline(time.Now().Add(c.Timeout))
	// }
	_, err := c.Conn.Write([]byte(req))
	if err != nil {
		c.Logger.Printf("error sending %s command: %s", cmd, err)
	}
	c.CSeq++
	c.PreviousCmd = cmd
	return nil
}

// Options sends the `OPTIONS` command
func (c *Info) Options() (RtspResponse, error) {
	err := c.sendRtspCmd("OPTIONS", nil)
	if err != nil {
		return RtspResponse{}, err
	}
	return c.getResponse()
}

// Describe sends the `DESCRIBE` command
func (c *Info) Describe() (RtspResponse, error) {

	headers := map[string]string{
		"Accept": "application/sdp",
	}
	err := c.sendRtspCmd("DESCRIBE", headers)
	if err != nil {
		return RtspResponse{}, err
	}
	return c.getResponse()

}

// Setup sends the `SETUP` command and returns the session captured, if sent
func (c *Info) Setup() (RtspResponse, error) {
	if c.PreviousCmd != "DESCRIBE" {
		c.Logger.Printf("it is recomended to send the DESCRIBE command before SETUP")
	}
	headers := map[string]string{
		"User-Agent": "rtsp_test (LIVE555 Streaming Media v2015.09.24)",
		"Transport":  "RTP/AVP/TCP;unicast;interleaved=0-1",
	}
	//TODO: make this not hardcoded
	c.Url = c.Url + "/track1"
	err := c.sendRtspCmd("SETUP", headers)
	if err != nil {
		return RtspResponse{}, err
	}

	res, err := c.getResponse()
	if err != nil {
		return RtspResponse{}, err
	}

	c.Session = res.Headers["Session"]
	if c.Session == "" {
		return res, errors.New("no session returned from RTSP Stream")
	}

	if res.Headers["timeout"] != "" {
		timeout, err := strconv.Atoi(res.Headers["timeout"])
		if err != nil {
			c.Logger.Printf("failed to convert string to int: %s", err)
		}
		c.Timeout = time.Duration(timeout)
	}

	return res, nil
}

// Play sends the `PLAY` command
func (c *Info) Play() (RtspResponse, error) {
	if c.Session == "" {
		return RtspResponse{}, errors.New("no session, issue command `SETUP` before `PLAY`")
	}

	headers := map[string]string{
		"User-Agent": "rtsp_test (LIVE555 Streaming Media v2015.09.24)",
		"Range":      "npt=0.000-",
	}
	err := c.sendRtspCmd("PLAY", headers)
	if err != nil {
		return RtspResponse{}, err
	}
	return RtspResponse{}, nil //c.getResponse()
}

func (c *Info) getResponse() (RtspResponse, error) {
	res := make([]byte, 4096)
	n, err := c.Conn.Read(res)

	if err != nil {
		c.Logger.Printf("error reading response from RTSP")
		return RtspResponse{}, err
	}

	if n == 0 {
		c.Logger.Printf("no response from RTSP")
		return RtspResponse{}, nil
	}

	c.Logger.Printf("Response: \r\n %s \r\n", res)
	resp, err := parseStringRtspResponse(string(res))
	c.PreviousResp = &resp
	return resp, err
}

func parseStringRtspResponse(strResp string) (RtspResponse, error) {
	var responseStruct RtspResponse

	if strResp == "" {
		return RtspResponse{}, nil
	}

	sections := strings.Split(strResp, "\r\n\r\n")
	if len(sections) == 0 {
		return RtspResponse{}, nil
	}

	headers := sections[0]
	if len(sections) > 1 {
		body := sections[1]
		responseStruct.Body = body
	}

	headerMap := make(map[string]string)
	for header := range strings.SplitSeq(headers, "\r\n") {
		splitHeader := strings.SplitN(header, ":", 2)
		if len(splitHeader) < 2 {
			continue
		}
		//TODO: this whole thing could be done a lot better, anything after a ';' is a
		// param, so maybe we store these somehow better? also, if it sends more that 2 params
		// this will shit itself
		if splitHeader[0] == "Session" {
			splitSessionParams := strings.Split(splitHeader[1], ";")
			if len(splitSessionParams) == 2 {
				session := strings.TrimSpace(splitSessionParams[0])
				timeout := strings.TrimSpace(splitSessionParams[1])

				headerMap["Session"] = session
				headerMap["timeout"] = strings.Split(timeout, "=")[1]
			}

		} else {
			key := strings.TrimSpace(splitHeader[0])
			val := strings.TrimSpace(splitHeader[1])
			headerMap[key] = val
		}
	}

	responseStruct.Headers = headerMap
	return responseStruct, nil
}

func (c *Info) setupFFmpegPipe() error {
	// FFmpeg command to process the incoming RTP stream
	cmd := exec.Command("ffmpeg", "-f", "h264", "-i", "-", "-c:v", "copy", "-f", "mp4", "output.mp4")

	// Set up a pipe to feed RTP data into FFmpeg
	ffmpegIn, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create FFmpeg pipe: %w", err)
	}

	// Start the FFmpeg process
	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	c.ffmpegCmd = cmd
	c.ffmpegIn = ffmpegIn

	return nil
}

func (c *Info) ReadRtpPacketAndStreamToFFmpeg() {
	// Open the FFmpeg pipe for the first time
	err := c.setupFFmpegPipe()
	if err != nil {
		log.Fatalf("Error setting up FFmpeg pipe: %s", err)
		return
	}
	defer c.ffmpegIn.Close()

	// Start reading RTP packets and piping them to FFmpeg
	buf := make([]byte, 1)
	for {
		// Buffer for the RTP packet
		_, err := io.ReadFull(c.Conn, buf)
		if err != nil {
			c.Logger.Printf("Error reading RTP packet header: %s", err)
			return
		}

		// If it's not an RTP packet, skip it and continue reading
		if buf[0] != '$' {
			c.Logger.Printf("Not an RTP packet")
			continue
		}

		header := make([]byte, 3)
		// Channel and payload length
		_, err = io.ReadFull(c.Conn, header)
		if err != nil {
			return
		}
		channel := header[0]
		length := int(header[1])<<8 | int(header[2])

		// Allocate buffer for payload data
		payload := make([]byte, length)
		_, err = io.ReadFull(c.Conn, payload)
		if err != nil {
			log.Fatalf("Read payload error: %v", err)
		}

		switch channel {
		case 0:
			// RTP media packet
			if len(payload) <= 12 {
				c.Logger.Printf("Payload too short for RTP: %d bytes", len(payload))
				continue
			}
			rtpHeaderLen := 12 + int(payload[0]&0x0F)*4 // base header + CSRC list
			if len(payload) < rtpHeaderLen {
				c.Logger.Printf("RTP header length exceeds payload size")
				continue
			}
			rtpPayload := payload[rtpHeaderLen:]
			c.handleRTPPayload(rtpPayload)

		case 1:
			// RTCP packet — ignore it silently, it's normal
			continue

		default:
			c.Logger.Printf("Unknown channel: %d, ignoring", channel)
			continue
		}
	}
}

var startCode = []byte{0x00, 0x00, 0x00, 0x01}

func (c *Info) handleRTPPayload(payload []byte) {
	if len(payload) < 1 {
		return
	}

	nalType := payload[0] & 0x1F
	c.Logger.Printf("Nal type: %d", nalType)

	switch nalType {
	case 7: // SPS
		if len(c.sps) == 0 {
			c.sps = make([]byte, len(startCode)+len(payload))
			copy(c.sps, startCode)
			copy(c.sps[len(startCode):], payload)
		}
		c.Logger.Printf("SPS: %x", c.sps)
	case 8: // PPS
		if len(c.pps) == 0 {
			c.pps = make([]byte, len(startCode)+len(payload))
			copy(c.pps, startCode)
			copy(c.pps[len(startCode):], payload)
		}
		c.Logger.Printf("PPS: %x", c.pps)
	case 5: // IDR (keyframe)
		if len(c.sps) == 0 {
			c.Logger.Print("Orphaned frame, dropping")
			return
		}
		c.saveNAL(c.sps)
		c.saveNAL(c.pps)
		c.saveNAL([]byte(startCode))
		c.saveNAL(payload)
	case 1, 6: // Non-IDR slice or SEI
		c.saveNAL(c.sps)
		c.saveNAL(c.pps)
		c.saveNAL([]byte(startCode))
		c.saveNAL(payload)
	case 28: // FU-A

		if len(payload) < 2 {
			return
		}

		fuIndicator := payload[0]
		fuHeader := payload[1]
		start := (fuHeader & 0x80) != 0
		end := (fuHeader & 0x40) != 0
		reconstructedNALType := fuHeader & 0x1F
		nalHeader := (fuIndicator & 0xE0) | reconstructedNALType

		if start {
			c.currentFU = []byte{nalHeader}
			c.currentFU = append(c.currentFU, payload[2:]...)
		} else if c.currentFU != nil {
			c.currentFU = append(c.currentFU, payload[2:]...)
		} else {
			c.Logger.Println("Orphaned FU-A fragment, dropping")
			return
		}

		if end {
			if len(c.sps) == 0 || len(c.pps) == 0 {
				c.Logger.Print("Orphaned FU-A end fragment without SPS/PPS, dropping")
				c.currentFU = nil
				return
			}

			if (c.currentFU[0] & 0x1F) == 5 { // IDR
				c.Logger.Println("FU-A completed: IDR slice, injecting SPS/PPS")
				c.Logger.Printf("SPS:%s\nPPS:%s", c.sps, c.pps)
				c.saveNAL(c.sps)
				c.saveNAL(c.pps)
			}

			c.saveNAL([]byte(startCode))
			c.saveNAL(c.currentFU)
			c.currentFU = nil
		}
	default:
		// Just ignore
	}
}

func (c *Info) findFirstDollarSign() bool {

	for {
		b := make([]byte, 1)
		_, err := io.ReadFull(c.Conn, b)

		if err != nil {
			c.Logger.Printf("error reading from stream")
			return false
		}

		if string(b[0]) == "$" {
			return true
		}
	}

}

func (c *Info) saveNAL(nal []byte) {
	file, err := os.OpenFile("output.h264", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Error opening file for appending: %s", err)
		return
	}
	defer file.Close()

	_, err = file.Write(nal)
	if err != nil {
		log.Printf("Error writing to file: %s", err)
	}
}

// func (c *Info) saveNAL(nal []byte) {
//
// 	// Write the RTP payload to FFmpeg's input pipe
// 	err := os.WriteFile("output.h264", nal, 0644)
// 	if err != nil {
// 		log.Printf("Error writing to file: %s", err)
// 	}
// 	// _, err = c.ffmpegIn.Write(nal)
// 	// if err != nil {
// 	// 	log.Printf("Error writing RTP data to FFmpeg: %s", err)
// 	// 	return
// 	// }
// }

func (c *Info) Close() error {
	var err error

	if c.ffmpegIn != nil {
		c.Logger.Println("Closing ffmpeg input pipe...")
		_ = c.ffmpegIn.Close() // ignore error, not worth losing sleep
	}

	if c.ffmpegCmd != nil {
		c.Logger.Println("Waiting for ffmpeg to finish...")
		if waitErr := c.ffmpegCmd.Wait(); waitErr != nil {
			c.Logger.Printf("ffmpeg exited with error: %v", waitErr)
			err = waitErr // record error to return
		}
	}

	//TODO: wrap this
	err = c.Conn.Close()
	return err
}
