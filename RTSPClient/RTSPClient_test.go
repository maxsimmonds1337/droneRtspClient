package RTSPClient

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"testing"
)

// Mock WriteCloser because bytes.Buffer doesn't implement Close()
type writeCloser struct {
	*bytes.Buffer
}

func (wc *writeCloser) Close() error {
	return nil
}

func newTestInfo(t *testing.T) *Info {
	t.Helper()
	devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)

	return &Info{
		Logger:   log.New(devNull, "", log.LstdFlags),
		ffmpegIn: &writeCloser{Buffer: &bytes.Buffer{}}, // Proper WriteCloser
	}
}

func TestParseStringRtspResponse(t *testing.T) {
	requiredSession := "F70043A6"
	requiredTimeout := "65"

	mockRtspResponse := fmt.Sprintf(
		"RTSP/1.0 200 OK\r\n"+
			"CSeq: 3\r\n"+
			"Date: Thu, Jan 01 1970 00:02:13 GMT\r\n"+
			"Transport: RTP/AVP;unicast;destination=192.168.201.20;source=192.168.201\r\n"+
			"Session: %s;timeout=%s\r\n"+
			"\r\n", requiredSession, requiredTimeout)

	rtspResponse, err := parseStringRtspResponse(mockRtspResponse)
	if err != nil {
		t.Error(err)
	}

	if rtspResponse.Headers == nil {
		t.Error("no headers parsed")
	}

	if actual := rtspResponse.Headers["Session"]; actual != requiredSession {
		t.Errorf("Session: want %s, got %s", requiredSession, actual)
	}

	if actual := rtspResponse.Headers["timeout"]; actual != requiredTimeout {
		t.Errorf("Timeout: want %s, got %s", requiredTimeout, actual)
	}
}

func TestParseStringRtspResponse_WithSDP(t *testing.T) {
	mockSDP := `v=0
o=- 3725543 1 IN IP4 192.168.201.1
s=Session streamed by "OnDemandRTSPServer"
i=H264VideoSMS
t=0 0
a=tool:LIVE555 Streaming Media v2015.07.23
a=type:broadcast
a=control:*
a=range:npt=0-
a=x-qt-text-nam:Session streamed by "OnDemandRTSPServer"
a=x-qt-text-inf:H264VideoSMS
m=video 0 RTP/AVP 96
c=IN IP4 0.0.0.0
b=AS:35000
a=rtpmap:96 H264/90000
a=fmtp:96 packetization-mode=1;profile-level-id=4D001F;sprop-parameter-sets=Z00AH+VAKALYgA==,aO4xEg==
a=control:track1`

	mockResponse := fmt.Sprintf(
		"RTSP/1.0 200 OK\r\n"+
			"CSeq: 2\r\n"+
			"Content-Base: rtsp://192.168.201.1:7070/H264VideoSMS/\r\n"+
			"Content-Type: application/sdp\r\n"+
			"Content-Length: %d\r\n"+
			"\r\n%s", len(mockSDP), mockSDP)

	resp, err := parseStringRtspResponse(mockResponse)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if resp.Body != mockSDP {
		t.Errorf("SDP Body mismatch")
	}
}

func TestHandleRTPPayload_SwitchCases(t *testing.T) {
	tests := []struct {
		name        string
		payload     []byte
		expectWrite bool
	}{
		{"SPS packet (nalType 7)", []byte{0x67, 0x64, 0x00, 0x1f}, false},
		{"PPS packet (nalType 8)", []byte{0x68, 0xee, 0x06, 0xf2}, false},
		{"IDR frame (nalType 5) after SPS/PPS", []byte{0x65, 0x88, 0x84, 0x21}, true},
		{"Non-IDR slice (nalType 1)", []byte{0x61, 0x9a, 0x22}, true},
		{"SEI slice (nalType 6)", []byte{0x66, 0xaa, 0xbb}, true},
		{"FU-A start fragment", []byte{0x7C, 0x85, 0xAA, 0xBB}, false},
		{"FU-A middle fragment", []byte{0x7C, 0x05, 0xAA, 0xBB}, false},
		{"FU-A end fragment", []byte{0x7C, 0x45, 0xAA, 0xBB}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := newTestInfo(t)

			// Clear the global vars
			info.currentFU = nil
			info.sps = []byte{0x00, 0x00, 0x00, 0x01, 0x67}
			info.pps = []byte{0x00, 0x00, 0x00, 0x01, 0x68}

			buf := &bytes.Buffer{}
			info.ffmpegIn = &writeCloser{Buffer: buf}

			if tt.name == "FU-A end fragment" {
				info.handleRTPPayload([]byte{0x7C, 0x85, 0xAA, 0xBB}) // Start
				info.handleRTPPayload([]byte{0x7C, 0x05, 0xAA, 0xBB}) // Middle
			}
			info.handleRTPPayload(tt.payload)

			gotWrite := buf.Len() > 0
			if gotWrite != tt.expectWrite {
				t.Errorf("%s: expected write %v, got %v", tt.name, tt.expectWrite, gotWrite)
			}
		})
	}
}
