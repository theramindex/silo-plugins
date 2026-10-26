package rewind

import (
	"bytes"
	"errors"
	"fmt"
	"time"
)

const (
	tsPacketSize       = 188
	h264StreamType     = 0x1b
	maxProbeBytes      = 8 << 20
	defaultSegmentSize = 3 << 20
)

var (
	errUnsupportedVideo = errors.New("live rewind supports H.264 MPEG-TS streams only")
	errSegmentTooLarge  = errors.New("live rewind segment exceeds the plugin transport limit")
)

type closedSegment struct {
	data     []byte
	duration time.Duration
}

type tsSegmenter struct {
	targetDuration  time.Duration
	maxSegmentSize  int
	carry           []byte
	pat             []byte
	pmt             []byte
	pmtPID          int
	videoPID        int
	videoType       byte
	probeBytes      int
	current         bytes.Buffer
	segmentStart    time.Time
	segmentStartPTS int64
	lastPTS         int64
	started         bool
}

func newTSSegmenter(target time.Duration) *tsSegmenter {
	if target < time.Second {
		target = 2 * time.Second
	}
	return &tsSegmenter{targetDuration: target, maxSegmentSize: defaultSegmentSize, pmtPID: -1, videoPID: -1}
}

func (s *tsSegmenter) feed(data []byte) ([]closedSegment, error) {
	if len(data) == 0 {
		return nil, nil
	}
	merged := append(append([]byte(nil), s.carry...), data...)
	start := findTSSync(merged)
	if start < 0 {
		if len(merged) > tsPacketSize*3 {
			s.carry = append([]byte(nil), merged[len(merged)-tsPacketSize*3:]...)
		} else {
			s.carry = merged
		}
		return nil, nil
	}
	merged = merged[start:]
	whole := len(merged) / tsPacketSize * tsPacketSize
	s.carry = append([]byte(nil), merged[whole:]...)
	var closed []closedSegment
	for offset := 0; offset < whole; offset += tsPacketSize {
		packet := append([]byte(nil), merged[offset:offset+tsPacketSize]...)
		segment, err := s.feedPacket(packet)
		if err != nil {
			return closed, err
		}
		if segment != nil {
			closed = append(closed, *segment)
		}
	}
	return closed, nil
}

func (s *tsSegmenter) feedPacket(packet []byte) (*closedSegment, error) {
	if len(packet) != tsPacketSize || packet[0] != 0x47 {
		return nil, nil
	}
	pid := int(packet[1]&0x1f)<<8 | int(packet[2])
	pusi := packet[1]&0x40 != 0
	payload, randomAccess := tsPayload(packet)
	if pid == 0 && pusi && len(payload) > 0 {
		if pmtPID := parsePAT(payload); pmtPID >= 0 {
			s.pmtPID = pmtPID
			s.pat = append([]byte(nil), packet...)
		}
	}
	if pid == s.pmtPID && pusi && len(payload) > 0 {
		videoPID, videoType := parsePMT(payload)
		if videoPID >= 0 {
			s.videoPID = videoPID
			s.videoType = videoType
			s.pmt = append([]byte(nil), packet...)
			if videoType != h264StreamType {
				return nil, errUnsupportedVideo
			}
		}
	}

	var pts int64 = -1
	keyframe := false
	if pid == s.videoPID && pusi && len(payload) > 0 {
		pts, keyframe = parseVideoPES(payload)
		keyframe = keyframe || randomAccess
		if pts >= 0 {
			s.lastPTS = pts
		}
	}

	if !s.started {
		s.probeBytes += len(packet)
		if s.probeBytes > maxProbeBytes && s.videoPID < 0 {
			return nil, errUnsupportedVideo
		}
		if pid != s.videoPID || !keyframe || len(s.pat) == 0 || len(s.pmt) == 0 {
			return nil, nil
		}
		s.startSegment(packet, pts)
		return nil, nil
	}

	if pid == s.videoPID && keyframe && s.segmentDuration(pts) >= s.targetDuration {
		segment, err := s.closeSegment(pts)
		if err != nil {
			return nil, err
		}
		s.startSegment(packet, pts)
		return segment, nil
	}
	if s.current.Len()+len(packet) > s.maxSegmentSize {
		return nil, errSegmentTooLarge
	}
	_, _ = s.current.Write(packet)
	return nil, nil
}

func (s *tsSegmenter) startSegment(packet []byte, pts int64) {
	s.current.Reset()
	_, _ = s.current.Write(s.pat)
	_, _ = s.current.Write(s.pmt)
	_, _ = s.current.Write(packet)
	s.segmentStart = time.Now()
	s.segmentStartPTS = pts
	s.started = true
}

func (s *tsSegmenter) closeSegment(nextPTS int64) (*closedSegment, error) {
	if s.current.Len() > s.maxSegmentSize {
		return nil, errSegmentTooLarge
	}
	duration := s.segmentDuration(nextPTS)
	if duration <= 0 {
		duration = time.Since(s.segmentStart)
	}
	if duration < 250*time.Millisecond {
		duration = s.targetDuration
	}
	return &closedSegment{data: append([]byte(nil), s.current.Bytes()...), duration: duration}, nil
}

func (s *tsSegmenter) segmentDuration(nextPTS int64) time.Duration {
	if nextPTS >= 0 && s.segmentStartPTS >= 0 {
		delta := nextPTS - s.segmentStartPTS
		if delta < 0 {
			delta += 1 << 33
		}
		return time.Duration(float64(delta) / 90000.0 * float64(time.Second))
	}
	if s.segmentStart.IsZero() {
		return 0
	}
	return time.Since(s.segmentStart)
}

func findTSSync(data []byte) int {
	for i := 0; i+tsPacketSize*2 < len(data); i++ {
		if data[i] == 0x47 && data[i+tsPacketSize] == 0x47 && data[i+tsPacketSize*2] == 0x47 {
			return i
		}
	}
	return -1
}

func tsPayload(packet []byte) ([]byte, bool) {
	adaptation := (packet[3] >> 4) & 0x03
	if adaptation == 0 || adaptation == 2 {
		return nil, false
	}
	offset := 4
	randomAccess := false
	if adaptation == 3 {
		if offset >= len(packet) {
			return nil, false
		}
		length := int(packet[offset])
		if length > 0 && offset+1 < len(packet) {
			randomAccess = packet[offset+1]&0x40 != 0
		}
		offset += 1 + length
	}
	if offset >= len(packet) {
		return nil, randomAccess
	}
	return packet[offset:], randomAccess
}

func psiSection(payload []byte) []byte {
	if len(payload) < 2 {
		return nil
	}
	pointer := int(payload[0])
	start := 1 + pointer
	if start+3 > len(payload) {
		return nil
	}
	sectionLength := int(payload[start+1]&0x0f)<<8 | int(payload[start+2])
	end := start + 3 + sectionLength
	if end > len(payload) {
		return nil
	}
	return payload[start:end]
}

func parsePAT(payload []byte) int {
	section := psiSection(payload)
	if len(section) < 12 || section[0] != 0x00 {
		return -1
	}
	for offset := 8; offset+4 <= len(section)-4; offset += 4 {
		program := int(section[offset])<<8 | int(section[offset+1])
		if program != 0 {
			return int(section[offset+2]&0x1f)<<8 | int(section[offset+3])
		}
	}
	return -1
}

func parsePMT(payload []byte) (int, byte) {
	section := psiSection(payload)
	if len(section) < 16 || section[0] != 0x02 {
		return -1, 0
	}
	programInfoLength := int(section[10]&0x0f)<<8 | int(section[11])
	offset := 12 + programInfoLength
	end := len(section) - 4
	for offset+5 <= end {
		streamType := section[offset]
		pid := int(section[offset+1]&0x1f)<<8 | int(section[offset+2])
		esInfoLength := int(section[offset+3]&0x0f)<<8 | int(section[offset+4])
		if streamType == h264StreamType || streamType == 0x24 || streamType == 0x02 {
			return pid, streamType
		}
		offset += 5 + esInfoLength
	}
	return -1, 0
}

func parseVideoPES(payload []byte) (int64, bool) {
	if len(payload) < 9 || payload[0] != 0 || payload[1] != 0 || payload[2] != 1 {
		return -1, false
	}
	headerLength := int(payload[8])
	esStart := 9 + headerLength
	pts := int64(-1)
	if payload[7]&0x80 != 0 && len(payload) >= 14 {
		pts = (int64(payload[9]&0x0e) << 29) |
			(int64(payload[10]) << 22) |
			(int64(payload[11]&0xfe) << 14) |
			(int64(payload[12]) << 7) |
			int64(payload[13]>>1)
	}
	if esStart >= len(payload) {
		return pts, false
	}
	return pts, containsH264IDR(payload[esStart:])
}

func containsH264IDR(data []byte) bool {
	for i := 0; i+4 < len(data); i++ {
		start := -1
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			start = i + 3
		} else if i+4 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
			start = i + 4
		}
		if start >= 0 && start < len(data) && data[start]&0x1f == 5 {
			return true
		}
	}
	return false
}

func segmenterErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
