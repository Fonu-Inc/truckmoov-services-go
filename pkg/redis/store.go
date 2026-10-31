package redis

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const defaultIdleConns = 128

type Store struct {
	addr     string
	password string
	db       string
	timeout  time.Duration
	idle     chan *pooledConn
}

type pooledConn struct {
	conn   net.Conn
	reader *bufio.Reader
}

// NewStore returns a client for addr. An empty password skips AUTH.
// Connections are reused from a small idle pool so hot write paths do not
// dial+AUTH on every command.
func NewStore(addr, password, db string) *Store {
	return &Store{
		addr:     strings.TrimSpace(addr),
		password: password,
		db:       strings.TrimSpace(db),
		timeout:  3 * time.Second,
		idle:     make(chan *pooledConn, defaultIdleConns),
	}
}

func (s *Store) Ping() error {
	reply, err := s.do("PING")
	if err != nil {
		return err
	}

	message, ok := reply.(string)
	if !ok || message != "PONG" {
		return fmt.Errorf("unexpected ping response %v", reply)
	}
	return nil
}

func (s *Store) Set(key string, value []byte) error {
	_, err := s.do("SET", key, string(value))
	return err
}

func (s *Store) SetEX(key string, value []byte, ttlSeconds int) error {
	_, err := s.do("SET", key, string(value), "EX", strconv.Itoa(ttlSeconds))
	return err
}

func (s *Store) SetNX(key string, value []byte, ttlSeconds int) (bool, error) {
	reply, err := s.do("SET", key, string(value), "EX", strconv.Itoa(ttlSeconds), "NX")
	if err != nil {
		return false, err
	}
	return reply != nil, nil
}

func (s *Store) Get(key string) ([]byte, bool, error) {
	reply, err := s.do("GET", key)
	return bulkValue("GET", reply, err)
}

// GetEX reads a value and pushes its expiry out to ttlSeconds from now, in one
// round trip.
func (s *Store) GetEX(key string, ttlSeconds int) ([]byte, bool, error) {
	reply, err := s.do("GETEX", key, "EX", strconv.Itoa(ttlSeconds))
	return bulkValue("GETEX", reply, err)
}

func bulkValue(command string, reply any, err error) ([]byte, bool, error) {
	if err != nil {
		return nil, false, err
	}
	if reply == nil {
		return nil, false, nil
	}

	value, ok := reply.(string)
	if !ok {
		return nil, false, fmt.Errorf("unexpected %s response type %T", command, reply)
	}
	return []byte(value), true, nil
}

func (s *Store) Del(keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	_, err := s.do("DEL", keys...)
	return err
}

func (s *Store) SAdd(key string, members ...string) error {
	args := make([]string, 0, len(members)+1)
	args = append(args, key)
	args = append(args, members...)
	_, err := s.do("SADD", args...)
	return err
}

func (s *Store) SRem(key string, members ...string) error {
	args := make([]string, 0, len(members)+1)
	args = append(args, key)
	args = append(args, members...)
	_, err := s.do("SREM", args...)
	return err
}

func (s *Store) SMembers(key string) ([]string, error) {
	reply, err := s.do("SMEMBERS", key)
	if err != nil {
		return nil, err
	}
	if reply == nil {
		return []string{}, nil
	}

	items, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected SMEMBERS response type %T", reply)
	}

	results := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected SMEMBERS item type %T", item)
		}
		results = append(results, text)
	}
	return results, nil
}

// MGet fetches many keys in one round trip. Missing keys are nil entries.
func (s *Store) MGet(keys ...string) ([][]byte, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	reply, err := s.do("MGET", keys...)
	if err != nil {
		return nil, err
	}
	if reply == nil {
		return make([][]byte, len(keys)), nil
	}
	items, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected MGET response type %T", reply)
	}
	out := make([][]byte, len(items))
	for i, item := range items {
		if item == nil {
			continue
		}
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected MGET item type %T", item)
		}
		out[i] = []byte(text)
	}
	return out, nil
}

// Pipeline runs commands on one connection and returns one reply per command.
func (s *Store) Pipeline(commands ...[]string) ([]any, error) {
	if len(commands) == 0 {
		return nil, nil
	}
	if s.addr == "" {
		return nil, errors.New("redis address is required")
	}

	pc, err := s.acquire()
	if err != nil {
		return nil, err
	}
	broken := true
	defer func() { s.release(pc, broken) }()

	for _, command := range commands {
		if len(command) == 0 {
			return nil, errors.New("empty redis command")
		}
		if err := writeCommand(pc.conn, command[0], command[1:]...); err != nil {
			return nil, err
		}
	}

	replies := make([]any, len(commands))
	for i := range commands {
		reply, err := readReply(pc.reader)
		if err != nil {
			return nil, err
		}
		replies[i] = reply
	}
	broken = false
	return replies, nil
}

const pushCappedScript = `redis.call('LPUSH', KEYS[1], ARGV[1])
redis.call('LTRIM', KEYS[1], 0, tonumber(ARGV[2]) - 1)
return redis.call('EXPIRE', KEYS[1], ARGV[3])`

// PushCapped puts value at the head of a list, keeps only the newest max
// entries and refreshes the expiry, atomically.
func (s *Store) PushCapped(key string, value []byte, max, ttlSeconds int) error {
	_, err := s.do("EVAL", pushCappedScript, "1", key, string(value), strconv.Itoa(max), strconv.Itoa(ttlSeconds))
	return err
}

// Range returns the first count entries of a list, head first.
func (s *Store) Range(key string, count int) ([][]byte, error) {
	reply, err := s.do("LRANGE", key, "0", strconv.Itoa(count-1))
	if err != nil || reply == nil {
		return nil, err
	}
	items, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected LRANGE response type %T", reply)
	}
	results := make([][]byte, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected LRANGE item type %T", item)
		}
		results = append(results, []byte(text))
	}
	return results, nil
}

func (s *Store) do(command string, args ...string) (any, error) {
	if s.addr == "" {
		return nil, errors.New("redis address is required")
	}

	pc, err := s.acquire()
	if err != nil {
		return nil, err
	}
	broken := true
	defer func() { s.release(pc, broken) }()

	if err := writeCommand(pc.conn, command, args...); err != nil {
		return nil, err
	}
	reply, err := readReply(pc.reader)
	if err != nil {
		return nil, err
	}
	broken = false
	return reply, nil
}

func (s *Store) acquire() (*pooledConn, error) {
	select {
	case pc := <-s.idle:
		if err := pc.conn.SetDeadline(time.Now().Add(s.timeout)); err != nil {
			_ = pc.conn.Close()
			return s.dial()
		}
		return pc, nil
	default:
		return s.dial()
	}
}

func (s *Store) release(pc *pooledConn, broken bool) {
	if pc == nil {
		return
	}
	if broken {
		_ = pc.conn.Close()
		return
	}
	select {
	case s.idle <- pc:
	default:
		_ = pc.conn.Close()
	}
}

func (s *Store) dial() (*pooledConn, error) {
	conn, err := net.DialTimeout("tcp", s.addr, s.timeout)
	if err != nil {
		return nil, err
	}

	if err := conn.SetDeadline(time.Now().Add(s.timeout)); err != nil {
		_ = conn.Close()
		return nil, err
	}

	reader := bufio.NewReader(conn)

	if s.password != "" {
		if err := writeCommand(conn, "AUTH", s.password); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if _, err := readReply(reader); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}

	if s.db != "" && s.db != "0" {
		if err := writeCommand(conn, "SELECT", s.db); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if _, err := readReply(reader); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}

	return &pooledConn{conn: conn, reader: reader}, nil
}

func writeCommand(w io.Writer, command string, args ...string) error {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, command)
	parts = append(parts, args...)

	if _, err := fmt.Fprintf(w, "*%d\r\n", len(parts)); err != nil {
		return err
	}
	for _, part := range parts {
		if _, err := fmt.Fprintf(w, "$%d\r\n%s\r\n", len(part), part); err != nil {
			return err
		}
	}
	return nil
}

func readReply(reader *bufio.Reader) (any, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}

	switch prefix {
	case '+':
		return readLine(reader)
	case '-':
		message, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		return nil, errors.New(message)
	case ':':
		value, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, err
		}
		return number, nil
	case '$':
		sizeText, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(sizeText)
		if err != nil {
			return nil, err
		}
		if size == -1 {
			return nil, nil
		}

		buf := make([]byte, size+2)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return nil, err
		}
		return string(buf[:size]), nil
	case '*':
		sizeText, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(sizeText)
		if err != nil {
			return nil, err
		}
		if size == -1 {
			return nil, nil
		}

		items := make([]any, 0, size)
		for i := 0; i < size; i++ {
			item, err := readReply(reader)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported redis reply prefix %q", prefix)
	}
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}
