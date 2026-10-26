package rewind

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTSSegmenterWithGeneratedH264MPEGTS(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not available for the optional real-stream fixture")
	}
	fixture := filepath.Join(t.TempDir(), "fixture.ts")
	command := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x180:rate=30", "-t", "7", "-c:v", "libx264", "-preset", "ultrafast", "-g", "30", "-keyint_min", "30", "-sc_threshold", "0", "-an", "-f", "mpegts", fixture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not create the optional fixture: %v (%s)", err, output)
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read generated fixture: %v", err)
	}
	segmenter := newTSSegmenter(2 * time.Second)
	var segments []closedSegment
	for offset := 0; offset < len(data); offset += 32768 {
		end := offset + 32768
		if end > len(data) {
			end = len(data)
		}
		closed, err := segmenter.feed(data[offset:end])
		if err != nil {
			t.Fatalf("segment generated fixture: %v", err)
		}
		segments = append(segments, closed...)
	}
	if len(segments) < 2 {
		t.Fatalf("expected multiple keyframe-aligned segments, got %d", len(segments))
	}
}

func TestTSSegmenterClosesKeyframeAlignedSegments(t *testing.T) {
	t.Parallel()
	segmenter := newTSSegmenter(2 * time.Second)
	stream := bytes.Join([][]byte{
		testPATPacket(), testPMTPacket(), testVideoPacket(0),
		testFillerPacket(), testVideoPacket(180000),
		testFillerPacket(), testVideoPacket(360000),
	}, nil)
	segments, err := segmenter.feed(stream)
	if err != nil {
		t.Fatalf("feed segmenter: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("expected two closed segments, got %d", len(segments))
	}
	for index, segment := range segments {
		if segment.duration != 2*time.Second {
			t.Fatalf("segment %d duration = %s", index, segment.duration)
		}
		if len(segment.data) < tsPacketSize*3 || len(segment.data)%tsPacketSize != 0 {
			t.Fatalf("segment %d is not packet aligned: %d bytes", index, len(segment.data))
		}
		if !bytes.Equal(segment.data[:tsPacketSize], testPATPacket()) {
			t.Fatalf("segment %d does not begin with PAT", index)
		}
	}
}

func TestTSSegmenterRejectsNonH264Video(t *testing.T) {
	t.Parallel()
	segmenter := newTSSegmenter(2 * time.Second)
	pmt := testPMTPacket()
	payload, _ := tsPayload(pmt)
	section := psiSection(payload)
	section[12] = 0x24
	_, err := segmenter.feed(bytes.Join([][]byte{testPATPacket(), pmt, testFillerPacket()}, nil))
	if err != errUnsupportedVideo {
		t.Fatalf("expected unsupported video error, got %v", err)
	}
}

func testPATPacket() []byte {
	section := []byte{0x00, 0xb0, 0x0d, 0x00, 0x01, 0xc1, 0x00, 0x00, 0x00, 0x01, 0xe1, 0x00, 0, 0, 0, 0}
	return testTSPacket(0, true, false, append([]byte{0}, section...))
}

func testPMTPacket() []byte {
	section := []byte{0x02, 0xb0, 0x12, 0x00, 0x01, 0xc1, 0x00, 0x00, 0xe1, 0x01, 0xf0, 0x00, 0x1b, 0xe1, 0x01, 0xf0, 0x00, 0, 0, 0, 0}
	return testTSPacket(0x100, true, false, append([]byte{0}, section...))
}

func testVideoPacket(pts int64) []byte {
	payload := []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 0x05}
	payload = append(payload, encodeTestPTS(pts)...)
	payload = append(payload, 0, 0, 1, 0x65, 0x88, 0x84)
	return testTSPacket(0x101, true, true, payload)
}

func testFillerPacket() []byte {
	return testTSPacket(0x101, false, false, bytes.Repeat([]byte{0xaa}, 160))
}

func testTSPacket(pid int, pusi, randomAccess bool, payload []byte) []byte {
	packet := bytes.Repeat([]byte{0xff}, tsPacketSize)
	packet[0] = 0x47
	packet[1] = byte((pid >> 8) & 0x1f)
	if pusi {
		packet[1] |= 0x40
	}
	packet[2] = byte(pid)
	offset := 4
	if randomAccess {
		packet[3] = 0x30
		packet[4] = 1
		packet[5] = 0x40
		offset = 6
	} else {
		packet[3] = 0x10
	}
	copy(packet[offset:], payload)
	return packet
}

func encodeTestPTS(pts int64) []byte {
	return []byte{
		byte(0x21 | ((pts >> 29) & 0x0e)),
		byte(pts >> 22),
		byte(((pts >> 14) & 0xfe) | 1),
		byte(pts >> 7),
		byte(((pts & 0x7f) << 1) | 1),
	}
}
