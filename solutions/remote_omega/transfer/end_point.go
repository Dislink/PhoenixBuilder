package transfer

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	zmq "github.com/go-zeromq/zmq4"
	"phoenixbuilder/lib/minecraft/neomega/omega"
	"phoenixbuilder/lib/minecraft/neomega/uqholder"
	"phoenixbuilder/minecraft/protocol"
	"phoenixbuilder/minecraft/protocol/packet"
	"time"
)

type Endpoint struct {
	sub    zmq.Socket
	caller *ZMQRpcCaller
	pool   packet.Pool
}

func NewEndPoint(ctx context.Context, pubAccessPoint, ctrlAccessPoint string) (endPoint *Endpoint, err error) {
	sub := zmq.NewSub(ctx)
	go func() {
		<-ctx.Done()
		sub.Close()
	}()
	err = sub.Dial(pubAccessPoint)
	if err != nil {
		return nil, err
	}
	err = sub.SetOption(zmq.OptionSubscribe, "packet")
	if err != nil {
		return nil, err
	}
	caller, err := NewZMQRpcCaller(nil, ctrlAccessPoint)
	if err != nil {
		panic(err)
	}
	return &Endpoint{
		pool:   packet.NewPool(),
		sub:    sub,
		caller: caller,
	}, nil
}

func (e *Endpoint) WaitReady() {
	for {
		if e.CheckAccessPointReady() {
			break
		}
		time.Sleep(time.Second / 10)
	}
}

func (e *Endpoint) CheckAccessPointReady() bool {
	ret := e.caller.BlockCallAndGet(nil, "botReady", nil)
	if len(ret) > 0 && string(ret[0]) == "botReady" {
		return true
	}
	return false
}

func (e *Endpoint) GetUQHolder() omega.MicroUQHolder {
	uqHolderData := e.caller.BlockCallAndGet(nil, "getUQHolderBytes", nil)
	uq, err := uqholder.NewMicroUQHolderFromData(uqHolderData[0])
	if err != nil {
		panic(err)
	}
	return uq
}

func (e *Endpoint) GetShieldID() int32 {
	shieldIDBytes := e.caller.BlockCallAndGet(nil, "getConnShieldID", nil)
	return int32(binary.LittleEndian.Uint32(shieldIDBytes[0]))
}

func (e *Endpoint) SendPacket(pk packet.Packet) {
	e.caller.CallNoResponse("sendPacket", [][]byte{RevertToRawPacketWithShield(pk)})
}

func (e *Endpoint) SendPacketData(pktID uint32, data []byte) {
	packetIDBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(packetIDBytes, pktID)
	e.caller.CallNoResponse("sendPacketBytes", [][]byte{packetIDBytes, data})
}

func (e *Endpoint) RecvPacket() (pk packet.Packet, shieldID int32, err error) {
	var msg zmq.Msg
	msg, err = e.sub.Recv()
	if err != nil {
		return nil, 0, err
	}
	shieldID = int32(binary.LittleEndian.Uint32(msg.Frames[2]))
	return ConvertFromRawPacketWithShield(e.pool, msg.Frames[1]), shieldID, err
}

func safeDecode(pkt packet.Packet, r *protocol.Reader) (p packet.Packet, err error) {
	defer func() {
		if recoveredErr := recover(); recoveredErr != nil {
			err = fmt.Errorf("%T: %w", pkt, recoveredErr.(error))
			//fmt.Println(err)
		}
	}()
	pkt.Unmarshal(r)
	return pkt, nil
}

func (e *Endpoint) RecvDirectPacket() (pk packet.Packet, shieldID int32, err error) {
	var msg zmq.Msg
	msg, err = e.sub.Recv()
	if err != nil {
		return nil, 0, err
	}
	shieldIDBytes, packetIDBytes, packetData, dataLenBytes := msg.Frames[1], msg.Frames[2], msg.Frames[3], msg.Frames[4]
	shieldID = int32(binary.LittleEndian.Uint32(shieldIDBytes))
	packetID := binary.LittleEndian.Uint32(packetIDBytes)
	dataLen := binary.LittleEndian.Uint32(dataLenBytes)
	if int(dataLen) != len(packetData) {
		return nil, 0, fmt.Errorf("len mismatch %v!=%v\n", int(dataLen), len(packetData))
	}
	reader := bytes.NewBuffer(packetData)
	r := protocol.NewReader(reader, shieldID)
	pkt := e.pool[packetID]()
	pkt, err = safeDecode(pkt, r)
	if err != nil {
		return nil, 0, err
	}
	return pkt, shieldID, nil
}