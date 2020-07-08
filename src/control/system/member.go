//
// (C) Copyright 2019-2020 Intel Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// GOVERNMENT LICENSE RIGHTS-OPEN SOURCE SOFTWARE
// The Government's rights to use, modify, reproduce, release, perform, display,
// or disclose this software are subject to the terms of the Apache License as
// provided in Contract No. 8F-30005.
// Any reproduction of computer software, computer software documentation, or
// portions thereof marked with this legend must also reproduce the markings.
//

package system

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/pkg/errors"

	"github.com/daos-stack/daos/src/control/logging"
)

// MemberState represents the activity state of DAOS system members.
type MemberState int

const (
	// MemberStateUnknown is the default invalid state.
	MemberStateUnknown MemberState = iota
	// MemberStateAwaitFormat indicates the member is waiting for format.
	MemberStateAwaitFormat
	// MemberStateStarting indicates the member has started but is not
	// ready.
	MemberStateStarting
	// MemberStateReady indicates the member has setup successfully.
	MemberStateReady
	// MemberStateJoined indicates the member has joined the system.
	MemberStateJoined
	// MemberStateStopping indicates prep-shutdown successfully run.
	MemberStateStopping
	// MemberStateStopped indicates process has been stopped.
	MemberStateStopped
	// MemberStateEvicted indicates rank has been evicted from DAOS system.
	MemberStateEvicted
	// MemberStateErrored indicates the process stopped with errors.
	MemberStateErrored
	// MemberStateUnresponsive indicates the process is not responding.
	MemberStateUnresponsive
)

func (ms MemberState) String() string {
	return [...]string{
		"Unknown",
		"AwaitFormat",
		"Starting",
		"Ready",
		"Joined",
		"Stopping",
		"Stopped",
		"Evicted",
		"Errored",
		"Unresponsive",
	}[ms]
}

// isTransitionIllegal indicates if given state transitions is legal.
//
// Map state combinations to true (illegal) or false (legal) and return negated
// value.
func (ms MemberState) isTransitionIllegal(to MemberState) bool {
	if ms == MemberStateUnknown {
		return true // no legal transitions
	}
	if ms == to {
		return true // identical state
	}
	return map[MemberState]map[MemberState]bool{
		MemberStateAwaitFormat: map[MemberState]bool{
			MemberStateEvicted: true,
		},
		MemberStateStarting: map[MemberState]bool{
			MemberStateEvicted: true,
		},
		MemberStateReady: map[MemberState]bool{
			MemberStateEvicted: true,
		},
		MemberStateJoined: map[MemberState]bool{
			MemberStateReady: true,
		},
		MemberStateStopping: map[MemberState]bool{
			MemberStateReady: true,
		},
		MemberStateEvicted: map[MemberState]bool{
			MemberStateReady:    true,
			MemberStateJoined:   true,
			MemberStateStopping: true,
		},
		MemberStateErrored: map[MemberState]bool{
			MemberStateReady:    true,
			MemberStateJoined:   true,
			MemberStateStopping: true,
		},
		MemberStateUnresponsive: map[MemberState]bool{
			MemberStateReady:    true,
			MemberStateJoined:   true,
			MemberStateStopping: true,
		},
	}[ms][to]
}

// Member refers to a data-plane instance that is a member of this DAOS
// system running on host with the control-plane listening at "Addr".
type Member struct {
	Rank  Rank
	UUID  uuid.UUID
	Addr  net.Addr
	URI   string
	state MemberState
	Info  string
}

// MarshalJSON marshals system.Member to JSON.
func (sm *Member) MarshalJSON() ([]byte, error) {
	// use a type alias to leverage the default marshal for
	// most fields
	type toJSON Member
	return json.Marshal(&struct {
		Addr  string
		State int
		*toJSON
	}{
		Addr:   sm.Addr.String(),
		State:  int(sm.state),
		toJSON: (*toJSON)(sm),
	})
}

// UnmarshalJSON unmarshals system.Member from JSON.
func (sm *Member) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}

	// use a type alias to leverage the default unmarshal for
	// most fields
	type fromJSON Member
	from := &struct {
		Addr  string
		State int
		*fromJSON
	}{
		fromJSON: (*fromJSON)(sm),
	}

	if err := json.Unmarshal(data, from); err != nil {
		return err
	}

	addr, err := net.ResolveTCPAddr("tcp", from.Addr)
	if err != nil {
		return err
	}
	sm.Addr = addr

	sm.state = MemberState(from.State)

	return nil
}

func (sm *Member) String() string {
	return fmt.Sprintf("%s/%d/%s", sm.Addr, sm.Rank, sm.State())
}

// State retrieves member state.
func (sm *Member) State() MemberState {
	return sm.state
}

// NewMember returns a reference to a new member struct.
func NewMember(rank Rank, uuidStr, uri string, addr net.Addr, state MemberState) *Member {
	// FIXME: Either require a valid uuid.UUID to be supplied
	// or else change the return signature to include an error
	newUUID := uuid.MustParse(uuidStr)
	return &Member{Rank: rank, UUID: newUUID, URI: uri, Addr: addr, state: state}
}

// Members is a type alias for a slice of member references
type Members []*Member

// MemberResult refers to the result of an action on a Member.
type MemberResult struct {
	Addr    string
	Rank    Rank
	Action  string
	Errored bool
	Msg     string
	State   MemberState
}

// MarshalJSON marshals system.MemberResult to JSON.
func (mr *MemberResult) MarshalJSON() ([]byte, error) {
	// use a type alias to leverage the default marshal for
	// most fields
	type toJSON MemberResult
	return json.Marshal(&struct {
		State int
		*toJSON
	}{
		State:  int(mr.State),
		toJSON: (*toJSON)(mr),
	})
}

// UnmarshalJSON unmarshals system.MemberResult from JSON.
func (mr *MemberResult) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}

	// use a type alias to leverage the default unmarshal for
	// most fields
	type fromJSON MemberResult
	from := &struct {
		State int
		*fromJSON
	}{
		fromJSON: (*fromJSON)(mr),
	}

	if err := json.Unmarshal(data, from); err != nil {
		return err
	}

	mr.State = MemberState(from.State)

	return nil
}

// NewMemberResult returns a reference to a new member result struct.
//
// Host address and action fields are not always used so not populated here.
func NewMemberResult(rank Rank, err error, state MemberState) *MemberResult {
	result := MemberResult{Rank: rank, State: state}
	if err != nil {
		result.Errored = true
		result.Msg = err.Error()
	}

	return &result
}

// MemberResults is a type alias for a slice of member result references.
type MemberResults []*MemberResult

// HasErrors returns true if any of the member results errored.
func (smr MemberResults) HasErrors() bool {
	for _, res := range smr {
		if res.Errored {
			return true
		}
	}

	return false
}

// Membership tracks details of system members.
type Membership struct {
	sync.RWMutex
	log logging.Logger
	db  *Database
}

func (m *Membership) addMember(member *Member) error {
	_, err := m.db.FindMemberByUUID(member.UUID)
	if err == nil {
		return FaultMemberExists(member.Rank)
	}
	m.log.Debugf("adding system member: %s", member)

	return m.db.AddMember(member)
}

func (m *Membership) updateMember(member *Member) error {
	old, err := m.db.FindMemberByUUID(member.UUID)
	m.log.Debugf("updating system member: %s->%s", old, member)
	if err != nil {
		return err
	}

	return m.db.AddMember(member)
}

// Add adds member to membership, returns member count.
func (m *Membership) Add(member *Member) (int, error) {
	m.Lock()
	defer m.Unlock()

	if err := m.addMember(member); err != nil {
		return -1, err
	}

	return m.db.MemberCount(), nil
}

// Count returns the number of members.
func (m *Membership) Count() int {
	return m.db.MemberCount()
}

type MemberJoinResult struct {
	Created    bool
	PrevState  MemberState
	MapVersion uint32
}

func (m *Membership) Join(newMember *Member) (res *MemberJoinResult, err error) {
	m.Lock()
	defer m.Unlock()

	defer func() {
		if err != nil {
			newMember.state = MemberStateErrored
		}
	}()

	newMember.state = MemberStateJoined
	r := new(MemberJoinResult)
	curMember, err := m.db.FindMemberByUUID(newMember.UUID)
	if err == nil {
		r.PrevState = curMember.state
		if err := m.db.UpdateMember(newMember); err != nil {
			return nil, err
		}
		r.MapVersion = m.db.CurMapVersion()

		return r, nil
	}

	r.Created = true
	if err := m.db.AddMember(newMember); err != nil {
		return nil, err
	}
	r.MapVersion = m.db.CurMapVersion()

	return r, nil
}

// AddOrReplace adds member to membership or replaces member if it exists.
//
// Note: this method updates state without checking if state transition is
//       legal so use with caution.
func (m *Membership) AddOrReplace(newMember *Member) error {
	m.Lock()
	defer m.Unlock()

	if err := m.addMember(newMember); err == nil {
		return nil
	}

	return m.updateMember(newMember)
}

// Remove removes member from membership, idempotent.
func (m *Membership) Remove(rank Rank) {
	m.Lock()
	defer m.Unlock()

	member, err := m.db.FindMemberByRank(rank)
	if err != nil {
		m.log.Errorf("remove %d failed: %s", rank, err)
		return
	}
	if err := m.db.RemoveMember(member); err != nil {
		m.log.Errorf("remove %d failed: %s", rank, err)
	}
}

// Get retrieves member reference from membership based on Rank.
func (m *Membership) Get(rank Rank) (*Member, error) {
	m.RLock()
	defer m.RUnlock()

	return m.db.FindMemberByRank(rank)
}

// Ranks returns slice of ordered member ranks.
func (m *Membership) Ranks(rankList ...Rank) (ranks []Rank) {
	m.RLock()
	defer m.RUnlock()

	for _, rank := range m.db.MemberRanks() {
		if len(rankList) != 0 && !rank.InList(rankList) {
			continue
		}

		ranks = append(ranks, rank)
	}

	sort.Slice(ranks, func(i, j int) bool { return ranks[i] < ranks[j] })

	return
}

// HostRanks returns mapping of control addresses to ranks managed by harness at
// that address.
//
// Filter to include only host keys with any of the provided ranks, if supplied.
func (m *Membership) HostRanks(rankList ...Rank) map[string][]Rank {
	m.RLock()
	defer m.RUnlock()

	hostRanks := make(map[string][]Rank)
	for _, member := range m.db.AllMembers() {
		addr := member.Addr.String()

		if len(rankList) != 0 && !member.Rank.InList(rankList) {
			continue
		}

		if _, exists := hostRanks[addr]; exists {
			hostRanks[addr] = append(hostRanks[addr], member.Rank)
			ranks := hostRanks[addr]
			sort.Slice(ranks, func(i, j int) bool { return ranks[i] < ranks[j] })
			continue
		}
		hostRanks[addr] = []Rank{member.Rank}
	}

	return hostRanks
}

// Hosts returns slice of control addresses that contain any of the ranks
// in the input rank list.
//
// If input rank list is empty, return all hosts in membership.
func (m *Membership) Hosts(rankList ...Rank) []string {
	hostRanks := m.HostRanks(rankList...)
	hosts := make([]string, 0, len(hostRanks))

	for host := range hostRanks {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)

	return hosts
}

// Members returns slice of references to all system members ordered by rank.
//
// Empty rank list implies no filtering/include all.
func (m *Membership) Members(rankList ...Rank) (ms Members) {
	ranks := m.Ranks(rankList...)

	m.RLock()
	defer m.RUnlock()

	for _, rank := range ranks {
		if len(rankList) != 0 && !rank.InList(rankList) {
			continue
		}

		if member, err := m.db.FindMemberByRank(rank); err == nil {
			ms = append(ms, member)
		}
	}

	m.log.Debugf("ms: %s", ms)
	return ms
}

// UpdateMemberStates updates member's state according to result state.
//
// If ignoreErrored is set, only update member state and info if result is a
// success (subsequent ping will update member state).
func (m *Membership) UpdateMemberStates(results MemberResults, ignoreErrored bool) error {
	m.Lock()
	defer m.Unlock()

	for _, result := range results {
		member, err := m.db.FindMemberByRank(result.Rank)
		if err != nil {
			return FaultMemberMissing(result.Rank)
		}

		// use opportunity to update host address in result
		if result.Addr == "" {
			result.Addr = member.Addr.String()
		}

		// don't update members if:
		// - result reports an error and ignoreErrored is set or
		// - if transition from current to result state is illegal
		if result.Errored {
			if ignoreErrored {
				continue
			}
			if result.State != MemberStateErrored {
				return errors.Errorf(
					"errored result for rank %d has conflicting state '%s'",
					result.Rank, result.State)
			}
		}
		if member.State().isTransitionIllegal(result.State) {
			continue
		}
		member.state = result.State
		member.Info = result.Msg

		if err := m.db.UpdateMember(member); err != nil {
			return err
		}
	}

	return nil
}

// NewMembership returns a reference to a new DAOS system membership.
func NewMembership(log logging.Logger, db *Database) *Membership {
	return &Membership{db: db, log: log}
}
