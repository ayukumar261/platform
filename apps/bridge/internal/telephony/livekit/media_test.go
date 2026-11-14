package livekit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/livekit/media-sdk"
)

func TestAudioInputWritesLittleEndianPCM(t *testing.T) {
	input := make(audioInput, 1)
	if err := input.WriteSample(media.PCM16Sample{1, -2}); err != nil {
		t.Fatal(err)
	}

	expected := []byte{0x01, 0x00, 0xFE, 0xFF}
	if audio := <-input; !bytes.Equal(audio, expected) {
		t.Fatalf("expected %v, got %v", expected, audio)
	}
}

func TestAudioInputDropsChunksWhenFull(t *testing.T) {
	input := make(audioInput, 1)
	input.WriteSample(media.PCM16Sample{1})
	if err := input.WriteSample(media.PCM16Sample{2}); err != nil {
		t.Fatalf("expected a full queue to drop the chunk without error, got %v", err)
	}

	if audio := <-input; audio[0] != 1 {
		t.Errorf("expected the first chunk to be kept, got %v", audio)
	}
	if len(input) != 0 {
		t.Error("expected the second chunk to be dropped")
	}
}

func TestReadAudioReturnsEOFWhenCallEnds(t *testing.T) {
	c := &connection{input: make(audioInput), done: make(chan struct{})}
	close(c.done)

	if _, err := c.ReadAudio(context.Background()); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestErrIsNilUntilCallEnds(t *testing.T) {
	failure := errors.New("disconnected")
	c := &connection{done: make(chan struct{}), err: failure}
	if err := c.Err(); err != nil {
		t.Fatalf("expected nil before the call ends, got %v", err)
	}

	close(c.done)
	if err := c.Err(); err != failure {
		t.Fatalf("expected %v after the call ends, got %v", failure, err)
	}
}
