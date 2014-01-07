package gotracker

import (
	"errors"
	"github.com/tumdum/bencoding"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
)

var (
	NilRequestError      = errors.New("nil request")
	MissingPortError     = errors.New("port is missing form request")
	MissingPeerIdError   = errors.New("peer_id is missing from request")
	MissingInfoHashError = errors.New("info_hash is missing from request")
	MalformedRemoteAddr  = errors.New("RemoteAddr seems to be broken")
)

type Peer struct {
	Ip     string `bencoding:"ip"`
	Port   int    `bencoding:"port"`
	PeerId string `bencoding:"id"`
}

type RequestData struct {
	Peer
	InfoHash string
	NumWant  *int
}

// Important: assumes that was already called
func extractRequestData(r *http.Request) (*RequestData, error) {
	if r == nil {
		return nil, NilRequestError
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return nil, MalformedRemoteAddr
	}
	port, err := strconv.Atoi(r.FormValue("port"))
	if err != nil {
		return nil, MissingPortError
	}
	peer_id := r.FormValue("peer_id")
	if peer_id == "" {
		return nil, MissingPeerIdError
	}
	info_hash := url.QueryEscape(r.FormValue("info_hash"))
	if info_hash == "" {
		return nil, MissingInfoHashError
	}
	ret := &RequestData{Peer: Peer{ip, port, peer_id}, InfoHash: info_hash}

	numWant, err := strconv.Atoi(r.FormValue("numwant"))
	if err == nil {
		ret.NumWant = &numWant
	}
	return ret, nil
}

type peerSet struct {
	m     sync.Mutex
	peers map[Peer]bool
}

func newPeerSet() *peerSet {
	ps := &peerSet{}
	ps.peers = make(map[Peer]bool)
	return ps
}

type Tracker struct {
	Logger          *log.Logger
	m               sync.Mutex
	managedTorrents map[string]*peerSet
	config          Server
}

func MakeTracker(logSink io.Writer, serverConfig *Server) *Tracker {
	t := new(Tracker)
	t.Logger = log.New(logSink, "gotracker ", log.LstdFlags)
	t.config = *serverConfig
	t.managedTorrents = make(map[string]*peerSet)
	return t
}

type TrackerResponse struct {
	Interval int    `bencoding:"interval"`
	Peers    []Peer `bencoding:"peers"`
}

func (t *Tracker) logAndFail(reason string, err error, w http.ResponseWriter) {
	t.Logger.Printf("Tracker failed due to '%v' with reason '%v'", err, reason)
	_, err = w.Write([]byte(reason))
	if err != nil {
		t.Logger.Printf("Failed to write response in logAndFail!")
	}
}

func (t *Tracker) addPeer(req *RequestData) {
	t.m.Lock()
	defer t.m.Unlock()
	if _, ok := t.managedTorrents[req.InfoHash]; !ok {
		t.managedTorrents[req.InfoHash] = newPeerSet()
	}
	t.managedTorrents[req.InfoHash].peers[req.Peer] = true
}

func (t *Tracker) collectPeers(req *RequestData) []Peer {
	ret := []Peer{}
	max := t.config.DefaultNumWant
	if req.NumWant != nil {
		max = *req.NumWant
	}
	if max == len(ret) {
		return ret
	}
	t.m.Lock()
	peers := t.managedTorrents[req.InfoHash].peers
	t.managedTorrents[req.InfoHash].m.Lock()
	t.m.Unlock()
	defer t.managedTorrents[req.InfoHash].m.Unlock()
	found := 0
	for k, v := range peers {
		if !v || k == req.Peer {
			continue
		}
		ret = append(ret, k)
		found++
		if found == max {
			break
		}
	}
	return ret
}

// Records req.Peer as one interested in req.Info hash and return other
// peers interested in that torrent.
func (t *Tracker) prepareResponse(req *RequestData) TrackerResponse {
	t.addPeer(req)
	return TrackerResponse{t.config.Interval, t.collectPeers(req)}
}

func (t *Tracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, err := extractRequestData(r)
	if err != nil {
		t.logAndFail("Incorrect request", err, w)
		return
	}
	resp := t.prepareResponse(req)
	b, _ := bencoding.Marshal(resp)
	_, err = w.Write(b)
	if err != nil {
		t.Logger.Printf("Failed to write response due to: '%v'", err)
	}
}
