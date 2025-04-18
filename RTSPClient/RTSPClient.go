package RTSPClient

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
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
	return c.getResponse()
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
	for {
		// Buffer for the RTP packet
		buf := make([]byte, 4)
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

		// Channel and payload length
		channel := buf[1]
		length := int(buf[2])<<8 | int(buf[3])

		// Allocate buffer for payload data
		payload := make([]byte, length)
		_, err = io.ReadFull(c.Conn, payload)
		if err != nil {
			log.Fatalf("Read payload error: %v", err)
		}

		c.Logger.Printf("Received RTP packet on channel %d with length %d bytes", channel, length)

		// If it's RTP data (channel 0), pipe it to FFmpeg
		if channel == 0 {
			// Write the RTP payload to FFmpeg's input pipe

			mediaPayload := payload[12:] // <-- strip off the first 12 bytes
			_, err := c.ffmpegIn.Write(mediaPayload)
			if err != nil {
				log.Printf("Error writing RTP data to FFmpeg: %s", err)
				return
			}
		}
	}
}

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

	c.Conn.Close()
	return err
}
