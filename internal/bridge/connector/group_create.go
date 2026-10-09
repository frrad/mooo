package connector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var _ bridgev2.GroupCreatingNetworkAPI = (*KakaoClient)(nil)

var errGroupCreateUnresolved = errors.New("connector: a group creation has an unresolved outcome; reconcile the selected Matrix room with the source group before creating or receiving new groups")

type groupCreateAttempt struct {
	Fingerprint     string  `json:"fingerprint"`
	Participants    []int64 `json:"participants"`
	ChatID          int64   `json:"chat_id,omitempty"`
	Rejected        bool    `json:"rejected,omitempty"`
	Bound           bool    `json:"bound,omitempty"`
	InviteAttempted bool    `json:"invite_attempted,omitempty"`
	Invitees        []int64 `json:"invitees,omitempty"`
}

func (kc *KakaoClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	if createChat {
		return nil, errors.New("connector: direct chat creation is not supported by this action")
	}
	userID, err := parseUserID(identifier)
	if err != nil {
		return nil, err
	}
	kc.mu.Lock()
	profile, ok := kc.profiles[userID]
	kc.mu.Unlock()
	if !ok {
		return nil, errors.New("connector: participant is not a previously resolved Kakao identity")
	}
	return &bridgev2.ResolveIdentifierResponse{UserID: makeUserID(userID), UserInfo: userInfoForMember(profile)}, nil
}

func (kc *KakaoClient) groupCreateRequest(params *bridgev2.GroupCreateParams) (chat.CreateRequest, string, error) {
	var request chat.CreateRequest
	if params == nil || params.Type != "regular" || !strings.HasPrefix(string(params.RoomID), "!") || !strings.Contains(string(params.RoomID), ":") {
		return request, "", errors.New("connector: regular group creation requires an explicit existing Matrix room")
	}
	if params.Username != "" || params.Parent != nil || params.Avatar != nil && params.Avatar.URL != "" || params.Topic != nil && params.Topic.Topic != "" || params.Disappear != nil && params.Disappear.Timer.Duration != 0 {
		return request, "", errors.New("connector: unsupported group creation option")
	}
	if params.Name != nil {
		request.Options.NickName = params.Name.Name
	}
	request.Options.PushAlert = true
	for _, participant := range params.Participants {
		userID, err := parseUserID(string(participant))
		if err != nil {
			return request, "", err
		}
		if userID == kc.userID {
			return request, "", errors.New("connector: select participants other than the creating account")
		}
		request.MemberIDs = append(request.MemberIDs, userID)
	}
	if len(request.MemberIDs) < 2 {
		return request, "", errors.New("connector: regular groups require at least two other participants")
	}
	if err := request.Validate(); err != nil {
		return request, "", err
	}
	slices.Sort(request.MemberIDs)
	b, err := json.Marshal(request)
	if err != nil {
		return request, "", err
	}
	sum := sha256.Sum256(b)
	return request, hex.EncodeToString(sum[:]), nil
}

func (kc *KakaoClient) createMetadata() (*UserLoginMetadata, error) {
	if kc.login == nil {
		return nil, bridgev2.ErrNotLoggedIn
	}
	meta, ok := kc.login.Metadata.(*UserLoginMetadata)
	if !ok {
		return nil, errors.New("connector: missing creation journal")
	}
	return meta, nil
}

func (kc *KakaoClient) saveGroupAttempt(ctx context.Context, room id.RoomID, attempt groupCreateAttempt) error {
	meta, err := kc.createMetadata()
	if err != nil {
		return err
	}
	// Replace the map instead of modifying a shared serialized value in place.
	next := make(map[string]groupCreateAttempt, len(meta.GroupCreates)+1)
	for key, value := range meta.GroupCreates {
		next[key] = value
	}
	next[string(room)] = attempt
	old := meta.GroupCreates
	meta.GroupCreates = next
	if err = kc.login.Save(ctx); err == nil {
		var saved *database.UserLogin
		saved, err = kc.login.Bridge.DB.UserLogin.GetByID(ctx, kc.login.ID)
		if err == nil {
			if saved == nil {
				err = errors.New("connector: creation journal login disappeared")
			} else {
				persisted, ok := saved.Metadata.(*UserLoginMetadata)
				if !ok {
					err = errors.New("connector: creation journal could not be verified")
				} else {
					got, present := persisted.GroupCreates[string(room)]
					if !present || got.Fingerprint != attempt.Fingerprint || got.ChatID != attempt.ChatID || got.Rejected != attempt.Rejected || got.Bound != attempt.Bound || got.InviteAttempted != attempt.InviteAttempted || !slices.Equal(got.Invitees, attempt.Invitees) || !slices.Equal(got.Participants, attempt.Participants) {
						err = errors.New("connector: creation journal update was not durable")
					}
				}
			}
		}
	}
	if err != nil {
		meta.GroupCreates = old
		return err
	}
	return nil
}

func (kc *KakaoClient) checkUnresolvedGroupCreates() error {
	if kc.login == nil {
		return nil
	}
	meta, err := kc.createMetadata()
	if err != nil {
		return err
	}
	for _, attempt := range meta.GroupCreates {
		if !attempt.Rejected && !attempt.Bound {
			return errGroupCreateUnresolved
		}
	}
	return nil
}

// CreateGroup is an explicit once-only mutation for the selected Matrix room.
// Confirmed attempts resume binding; uncertain attempts never issue CREATE again.
func (kc *KakaoClient) CreateGroup(ctx context.Context, params *bridgev2.GroupCreateParams) (*bridgev2.CreateChatResponse, error) {
	if ctx == nil {
		return nil, errors.New("connector: group creation requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, fingerprint, err := kc.groupCreateRequest(params)
	if err != nil {
		return nil, err
	}
	kc.groupGate.Lock()
	defer kc.groupGate.Unlock()
	meta, err := kc.createMetadata()
	if err != nil {
		return nil, err
	}
	c, err := kc.metadataClient()
	if err != nil {
		return nil, err
	}
	attempt, exists := meta.GroupCreates[string(params.RoomID)]
	if exists {
		if attempt.Fingerprint != fingerprint {
			return nil, errors.New("connector: this room already has a different group creation attempt")
		}
		if attempt.Rejected {
			return nil, errors.New("connector: source rejected this creation; no automatic retry")
		}
		if attempt.ChatID == 0 {
			return nil, errGroupCreateUnresolved
		}
	} else {
		if err = kc.checkUnresolvedGroupCreates(); err != nil {
			return nil, err
		}
		if err = kc.validateGroupRoom(ctx, params, nil); err != nil {
			return nil, err
		}
		attempt = groupCreateAttempt{Fingerprint: fingerprint, Participants: slices.Clone(request.MemberIDs)}
		if err = kc.saveGroupAttempt(ctx, params.RoomID, attempt); err != nil {
			return nil, err
		}
		response, createErr := c.CreateChat(ctx, request)
		if createErr != nil {
			var status client.StatusError
			if errors.As(createErr, &status) && status.Command == chat.CreateCommand {
				attempt.Rejected = true
				if err = kc.saveGroupAttempt(ctx, params.RoomID, attempt); err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("connector: group creation rejected (status %d); not retried", status.Status)
			}
			return nil, errGroupCreateUnresolved
		}
		if response.ChatID <= 0 {
			return nil, errGroupCreateUnresolved
		}
		attempt.ChatID = response.ChatID
		if err = kc.saveGroupAttempt(ctx, params.RoomID, attempt); err != nil {
			return nil, errGroupCreateUnresolved
		}
	}
	result, bindErr := kc.bindCreatedGroup(ctx, c, params.RoomID, attempt)
	if bindErr != nil {
		return nil, fmt.Errorf("connector: source group %d is confirmed; binding remains pending and CREATE will not be repeated: %w", attempt.ChatID, bindErr)
	}
	return result, nil
}

func (kc *KakaoClient) validateGroupRoom(ctx context.Context, params *bridgev2.GroupCreateParams, allowedKey *networkid.PortalKey) error {
	br := kc.login.Bridge
	if br == nil || br.Matrix == nil {
		return errors.New("connector: Matrix room validation is unavailable")
	}
	existing, err := br.GetPortalByMXID(ctx, params.RoomID)
	if err != nil {
		return err
	}
	if existing != nil && (allowedKey == nil || existing.PortalKey != *allowedKey) {
		return errors.New("connector: selected Matrix room is already bridged")
	}
	provider, ok := br.Matrix.(bridgev2.MatrixConnectorWithArbitraryRoomState)
	if !ok {
		return errors.New("connector: Matrix room permission validation is unavailable")
	}
	powerEvent, err := provider.GetStateEvent(ctx, params.RoomID, event.StatePowerLevels, "")
	if err != nil {
		return err
	}
	if powerEvent == nil {
		return errors.New("connector: room power levels are unavailable")
	}
	power, ok := powerEvent.Content.Parsed.(*event.PowerLevelsEventContent)
	if !ok || power == nil {
		return errors.New("connector: room power levels could not be validated")
	}
	createEvent, err := provider.GetStateEvent(ctx, params.RoomID, event.StateCreate, "")
	if err != nil {
		return err
	}
	if createEvent == nil {
		return errors.New("connector: room creation state is unavailable")
	}
	power = power.Clone()
	power.CreateEvent = createEvent
	if power.GetUserLevel(kc.login.UserMXID) < power.GetEventLevel(event.StateBridge) {
		return errors.New("connector: login owner lacks permission to bridge the selected room")
	}
	botLevel := power.GetUserLevel(br.Bot.GetMXID())
	for _, typ := range []event.Type{event.StateBridge, event.StateRoomName, event.StateRoomAvatar, event.StateTopic, event.StateMember, event.StateElementFunctionalMembers} {
		if botLevel < power.GetEventLevel(typ) {
			return errors.New("connector: bridge bot lacks required room state permissions")
		}
	}
	if botLevel < power.Invite() {
		return errors.New("connector: bridge bot cannot invite selected source identities")
	}

	members, err := br.Matrix.GetMembers(ctx, params.RoomID)
	if err != nil {
		return err
	}
	if members[kc.login.UserMXID] == nil || members[kc.login.UserMXID].Membership != event.MembershipJoin || members[br.Bot.GetMXID()] == nil || members[br.Bot.GetMXID()].Membership != event.MembershipJoin {
		return errors.New("connector: login owner and bridge bot must be joined to the selected room")
	}
	allowed := map[id.UserID]bool{kc.login.UserMXID: true, br.Bot.GetMXID(): true}
	// A prior partial binding may already have added the creating account's ghost.
	if allowedKey != nil {
		selfGhost, err := br.GetGhostByID(ctx, makeUserID(kc.userID))
		if err != nil {
			return err
		}
		allowed[selfGhost.Intent.GetMXID()] = true
	}
	for _, participant := range params.Participants {
		ghost, err := br.GetGhostByID(ctx, participant)
		if err != nil {
			return err
		}
		allowed[ghost.Intent.GetMXID()] = true
		member := members[ghost.Intent.GetMXID()]
		if member == nil || !member.Membership.IsInviteOrJoin() {
			return errors.New("connector: all selected Kakao ghosts must be invited or joined")
		}
	}
	for userID, member := range members {
		if member.Membership.IsInviteOrJoin() && !allowed[userID] {
			return errors.New("connector: selected room contains an unselected participant")
		}
	}
	return nil
}

func (kc *KakaoClient) bindCreatedGroup(ctx context.Context, c kakaoClient, room id.RoomID, attempt groupCreateAttempt) (*bridgev2.CreateChatResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	key := makePortalKey(attempt.ChatID, kc.login.ID)
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: room}
	for _, participant := range attempt.Participants {
		params.Participants = append(params.Participants, makeUserID(participant))
	}
	if err := kc.validateGroupRoom(ctx, params, &key); err != nil {
		return nil, err
	}
	portal, err := kc.login.Bridge.GetPortalByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	info, err := kc.chatInfoFromClient(ctx, portal, c, true)
	if err != nil {
		return nil, err
	}
	expected := make(map[networkid.UserID]bool, len(attempt.Participants)+1)
	expected[makeUserID(kc.userID)] = true
	for _, participant := range attempt.Participants {
		expected[makeUserID(participant)] = true
	}
	rawRoster, err := kc.creationSourceRoster(ctx, c, attempt.ChatID, attempt.Participants)
	if err != nil {
		return nil, err
	}
	if len(rawRoster) != len(expected) {
		return nil, errors.New("connector: source roster is incomplete for group binding")
	}
	if info.Members == nil || !info.Members.IsFull || len(info.Members.MemberMap) != len(expected) {
		return nil, errors.New("connector: created group roster differs from selected participants")
	}
	for userID := range info.Members.MemberMap {
		if !expected[userID] {
			return nil, errors.New("connector: created group has an unselected source participant")
		}
	}
	if portal.MXID != "" && portal.MXID != room {
		return nil, errors.New("connector: source group is already bound to another Matrix room; reconcile explicitly")
	}
	if err = portal.UpdateMatrixRoomID(ctx, room, bridgev2.UpdateMatrixRoomIDParams{FailIfMXIDSet: true}); err != nil {
		return nil, err
	}
	// UpdateMatrixRoomID may have changed its cache before a failed DB save.
	// Persist even if a repeated call sees an unchanged in-memory MXID.
	if err = portal.Save(ctx); err != nil {
		return nil, err
	}
	portal.UpdateInfo(ctx, info, kc.login, nil, time.Time{})
	if err = kc.verifyGroupBinding(ctx, portal, info, expected); err != nil {
		return nil, err
	}
	if err = portal.Save(ctx); err != nil {
		return nil, err
	}
	persisted, err := kc.login.Bridge.DB.Portal.GetByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	if persisted == nil || persisted.MXID != room {
		return nil, errors.New("connector: group room binding was not persisted")
	}
	attempt.Bound = true
	if err = kc.saveGroupAttempt(ctx, room, attempt); err != nil {
		return nil, err
	}
	return &bridgev2.CreateChatResponse{PortalKey: key, Portal: portal, PortalInfo: info}, nil
}

// creationSourceRoster checks source identities independently of the framework's
// member map, which includes the local account even when MEMLIST omits it.
func (kc *KakaoClient) creationSourceRoster(ctx context.Context, c kakaoClient, chatID int64, participants []int64) (map[int64]bool, error) {
	roster, err := c.MemberList(ctx, chatID, 0)
	if err != nil {
		return nil, err
	}
	allowed := map[int64]bool{kc.userID: true}
	for _, participant := range participants {
		allowed[participant] = true
	}
	actual := map[int64]bool{}
	for _, member := range roster.MemberIDs {
		if member <= 0 || actual[member] || !allowed[member] {
			return nil, errors.New("connector: source roster contains an invalid, duplicate or unselected identity")
		}
		actual[member] = true
	}
	if !actual[kc.userID] {
		return nil, errors.New("connector: creating account is absent from the source roster")
	}
	return actual, nil
}

func (kc *KakaoClient) resumeGroupCreates(ctx context.Context, c kakaoClient) error {
	kc.groupGate.Lock()
	defer kc.groupGate.Unlock()
	if kc.login == nil {
		return nil
	}
	meta, err := kc.createMetadata()
	if err != nil {
		return err
	}
	for room, attempt := range meta.GroupCreates {
		if attempt.Rejected || attempt.Bound {
			continue
		}
		if attempt.ChatID == 0 {
			return permanentAdmissionError{err: errGroupCreateUnresolved}
		}
		if _, err = kc.bindCreatedGroup(ctx, c, id.RoomID(room), attempt); err != nil {
			return err
		}
	}
	return nil
}

// Framework metadata updates log failures rather than returning them. Creation
// must verify the result before acknowledging its durable binding journal.
func (kc *KakaoClient) verifyGroupBinding(ctx context.Context, portal *bridgev2.Portal, info *bridgev2.ChatInfo, expected map[networkid.UserID]bool) error {
	if info.Name != nil && (!portal.NameSet || portal.Name != *info.Name) {
		return errors.New("connector: created group name has not converged in Matrix")
	}
	if info.Topic != nil && (!portal.TopicSet || portal.Topic != *info.Topic) {
		return errors.New("connector: created group topic has not converged in Matrix")
	}
	if info.Avatar != nil && !portal.AvatarSet {
		return errors.New("connector: created group avatar has not converged in Matrix")
	}
	members, err := kc.login.Bridge.Matrix.GetMembers(ctx, portal.MXID)
	if err != nil {
		return err
	}
	for userID := range expected {
		ghost, err := kc.login.Bridge.GetGhostByID(ctx, userID)
		if err != nil {
			return err
		}
		member := members[ghost.Intent.GetMXID()]
		if member == nil || member.Membership != event.MembershipJoin {
			return errors.New("connector: created group roster has not converged in Matrix")
		}
	}
	return nil
}
