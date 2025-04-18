package RTSPClient

import (
	"fmt"
	"strings"
	"testing"
)

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
	if rtspResponse.Headers == nil {
		t.Error("didn't get a response")
	}
	actualSession := rtspResponse.Headers["Session"]
	if err != nil {
		t.Error(err)
	}

	if actualSession != requiredSession {
		t.Errorf("session not correctly capture, wanted %s but got %s", requiredSession, actualSession)
	}

	actualTimeout := rtspResponse.Headers["timeout"]
	if actualTimeout != requiredTimeout {
		t.Errorf("timeout not correctly captured, wanted %s but got %s", requiredTimeout, actualTimeout)
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
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.Body != mockSDP {
		t.Errorf("Expected SDP body to be:\n%s\nBut got:\n%s", mockSDP, resp.Body)
	}

	// If you're feeling spicy, check for a couple of expected fields manually
	if !strings.Contains(resp.Body, "a=rtpmap:96 H264/90000") {
		t.Error("Missing expected rtpmap in SDP body")
	}
	if !strings.Contains(resp.Body, "a=control:track1") {
		t.Error("Missing expected control track in SDP body")
	}
	if !strings.Contains(resp.Body, "m=video 0 RTP/AVP 96") {
		t.Error("Missing expected media description")
	}
}
