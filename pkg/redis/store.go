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

type Store struct {
	addr    string
	db      string
	timeout time.Duration
}

func NewStore(addr, db string) *Store {
	return &Store{
		addr:    strings.TrimSpace(addr),
		db:      strings.TrimSpace(db),
		timeout: 3 * time.Second,
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

func (s *Store) Get(key string) ([]byte, bool, error) {
	reply, err := s.do("GET", key)
	if err != nil {
		return nil, false, err
	}
	if reply == nil {
		return nil, false, nil
	}

	value, ok := reply.(string)
	if !ok {
		return nil, false, fmt.Errorf("unexpected GET response type %T", reply)
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

func (s *Store) do(command string, args ...string) (any, error) {
	if s.addr == "" {
		return nil, errors.New("redis address is required")
	}

	conn, err := net.DialTimeout("tcp", s.addr, s.timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(s.timeout)); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(conn)

	if s.db != "" && s.db != "0" {
		if err := writeCommand(conn, "SELECT", s.db); err != nil {
			return nil, err
		}
		if _, err := readReply(reader); err != nil {
			return nil, err
		}
	}

	if err := writeCommand(conn, command, args...); err != nil {
		return nil, err
	}
	return readReply(reader)
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
