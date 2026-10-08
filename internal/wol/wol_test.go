package wol

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

func TestPacketRepeatsTheMACSixteenTimes(t *testing.T) {
	packet, err := Packet("B0:6E:BF:60:6D:64")
	if err != nil {
		t.Fatal(err)
	}
	mac, _ := net.ParseMAC("B0:6E:BF:60:6D:64")
	want := append(bytes.Repeat([]byte{0xff}, 6), bytes.Repeat(mac, 16)...)
	if !bytes.Equal(packet, want) {
		t.Fatal("unexpected magic packet")
	}
	if _, err := Packet("not-a-mac"); err == nil {
		t.Fatal("accepted an invalid MAC")
	}
}

func TestSendDeliversThePacket(t *testing.T) {
	listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Send(ctx, "B0:6E:BF:60:6D:64", listener.LocalAddr().String()); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 200)
	_ = listener.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := listener.ReadFrom(buffer)
	if err != nil || n != 102 {
		t.Fatal(n, err)
	}
}
