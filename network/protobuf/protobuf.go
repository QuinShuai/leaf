package protobuf

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/QuinShuai/leaf/chanrpc"
	"github.com/QuinShuai/leaf/log"
	"github.com/golang/protobuf/proto"
)

// -------------------------
// | id | protobuf message |
// -------------------------
type Processor struct {
	littleEndian bool
	msgInfo      map[uint32]*MsgInfo
	msgID        map[reflect.Type]uint32
	lastMsg      interface{}
	lastMsgData  []byte
}

type MsgInfo struct {
	msgType       reflect.Type
	msgRouter     *chanrpc.Server
	msgHandler    MsgHandler
	msgRawHandler MsgHandler
}

type MsgHandler func([]interface{})

type MsgRaw struct {
	msgID      uint32
	msgRawData []byte
}

func NewProcessor() *Processor {
	p := new(Processor)
	p.littleEndian = false
	p.msgInfo = make(map[uint32]*MsgInfo)
	p.msgID = make(map[reflect.Type]uint32)
	return p
}

// It's dangerous to call the method on routing or marshaling (unmarshaling)
func (p *Processor) SetByteOrder(littleEndian bool) {
	p.littleEndian = littleEndian
}

// It's dangerous to call the method on routing or marshaling (unmarshaling)
func (p *Processor) Register(msgId uint32, msg proto.Message) uint32 {
	msgType := reflect.TypeOf(msg)
	if msgType == nil || msgType.Kind() != reflect.Ptr {
		log.Fatal("protobuf message pointer required")
	}
	if _, ok := p.msgID[msgType]; ok {
		log.Fatal("message %s is already registered", msgType)
	}
	if len(p.msgInfo) >= math.MaxUint32 {
		log.Fatal("too many protobuf messages (max = %v)", math.MaxUint32)
	}

	i := new(MsgInfo)
	i.msgType = msgType
	p.msgInfo[msgId] = i
	p.msgID[msgType] = msgId
	return msgId
}

// It's dangerous to call the method on routing or marshaling (unmarshaling)
func (p *Processor) SetRouter(msg proto.Message, msgRouter *chanrpc.Server) {
	msgType := reflect.TypeOf(msg)
	id, ok := p.msgID[msgType]
	if !ok {
		log.Fatal("message %s not registered", msgType)
	}
	p.msgInfo[id].msgRouter = msgRouter
}

// It's dangerous to call the method on routing or marshaling (unmarshaling)
func (p *Processor) SetHandler(msg proto.Message, msgHandler MsgHandler) {
	msgType := reflect.TypeOf(msg)
	id, ok := p.msgID[msgType]
	if !ok {
		log.Fatal("message %s not registered", msgType)
	}

	p.msgInfo[id].msgHandler = msgHandler
}

// It's dangerous to call the method on routing or marshaling (unmarshaling)
func (p *Processor) SetRawHandler(id uint32, msgRawHandler MsgHandler) {
	if id >= uint32(len(p.msgInfo)) {
		log.Fatal("message id %v not registered", id)
	}

	p.msgInfo[id].msgRawHandler = msgRawHandler
}

// goroutine safe
func (p *Processor) Route(msg interface{}, userData interface{}) error {
	// raw
	if msgRaw, ok := msg.(MsgRaw); ok {
		if msgRaw.msgID >= uint32(len(p.msgInfo)) {
			return fmt.Errorf("message id %v not registered", msgRaw.msgID)
		}
		i := p.msgInfo[msgRaw.msgID]
		if i.msgRawHandler != nil {
			i.msgRawHandler([]interface{}{msgRaw.msgID, msgRaw.msgRawData, userData})
		}
		return nil
	}

	// protobuf
	msgType := reflect.TypeOf(msg)
	id, ok := p.msgID[msgType]
	if !ok {
		return fmt.Errorf("message %s not registered", msgType)
	}
	i := p.msgInfo[id]
	if i.msgHandler != nil {
		i.msgHandler([]interface{}{msg, userData})
	}
	if i.msgRouter != nil {
		i.msgRouter.Go(msgType, msg, userData)
	}
	return nil
}

// goroutine safe
func (p *Processor) Unmarshal(data []byte) (interface{}, error) {
	if len(data) < 4 {
		return nil, errors.New("protobuf data too short")
	}

	// id
	var id uint32
	if p.littleEndian {
		id = binary.LittleEndian.Uint32(data)
	} else {
		id = binary.BigEndian.Uint32(data)
	}

	// msg
	i := p.msgInfo[id]
	if i.msgRawHandler != nil {
		return MsgRaw{id, data[4:]}, nil
	} else {
		msg := reflect.New(i.msgType.Elem()).Interface()
		return msg, proto.UnmarshalMerge(data[4:], msg.(proto.Message))
	}
}

// goroutine safe
func (p *Processor) Marshal(msg interface{}) ([][]byte, error) {
	msgType := reflect.TypeOf(msg)

	// id
	_id, ok := p.msgID[msgType]
	if !ok {
		err := fmt.Errorf("message %s not registered", msgType)
		return nil, err
	}

	id := make([]byte, 4)
	if p.littleEndian {
		binary.LittleEndian.PutUint32(id, _id)
	} else {
		binary.BigEndian.PutUint32(id, _id)
	}

	// data
	data, err := proto.Marshal(msg.(proto.Message))

	if strings.Contains(msgType.String(), "Gzip") {
		if p.lastMsg == msg {
			data = p.lastMsgData
		} else {
			fmt.Println("压缩前数据大小:", len(data))

			var in bytes.Buffer
			w, _ := gzip.NewWriterLevel(&in, gzip.BestSpeed)
			w.Write(data)
			w.Close()

			fmt.Println("压缩后数据大小:", len(in.Bytes()))
			data = in.Bytes()
			p.lastMsg = msg
			p.lastMsgData = data
		}
	}

	return [][]byte{id, data}, err
}

// goroutine safe
func (p *Processor) Range(f func(id uint32, t reflect.Type)) {
	for id, i := range p.msgInfo {
		f(uint32(id), i.msgType)
	}
}
