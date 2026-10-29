package playerapi

import (
	"context"
	"strings"
	"sync"
	"time"

	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// privateMatchRuleLifetime bounds how long a room password keeps its rule.
const privateMatchRuleLifetime = time.Hour

// pvpCasualServer serves the private-room rules shared between local players.
// Matching itself stays on the descriptor compatibility path.
type pvpCasualServer struct {
	api.UnimplementedPvpCasualServer
	rules *privateMatchRules
}

type privateMatchRule struct {
	setting   *api.PrivateMatchRuleSetting
	expiresAt time.Time
}

type privateMatchRules struct {
	mu    sync.Mutex
	now   func() time.Time
	rules map[string]privateMatchRule
}

func newPrivateMatchRules() *privateMatchRules {
	return &privateMatchRules{now: time.Now, rules: make(map[string]privateMatchRule)}
}

// setup stores a copy of setting and reports whether the password had no live rule.
func (r *privateMatchRules) setup(password string, setting *api.PrivateMatchRuleSetting) (*api.PrivateMatchRuleSetting, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.prune(now)
	_, exists := r.rules[password]
	if setting == nil {
		setting = &api.PrivateMatchRuleSetting{}
	}
	stored := proto.Clone(setting).(*api.PrivateMatchRuleSetting)
	r.rules[password] = privateMatchRule{setting: stored, expiresAt: now.Add(privateMatchRuleLifetime)}
	return proto.Clone(stored).(*api.PrivateMatchRuleSetting), !exists
}

func (r *privateMatchRules) get(password string) (*api.PrivateMatchRuleSetting, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(r.now())
	rule, ok := r.rules[password]
	if !ok {
		return nil, false
	}
	return proto.Clone(rule.setting).(*api.PrivateMatchRuleSetting), true
}

func (r *privateMatchRules) prune(now time.Time) {
	for password, rule := range r.rules {
		if !now.Before(rule.expiresAt) {
			delete(r.rules, password)
		}
	}
}

func privateMatchPassword(value string) (string, error) {
	password := strings.TrimSpace(value)
	if password == "" {
		return "", status.Error(codes.InvalidArgument, "password is required")
	}
	return password, nil
}

func (s pvpCasualServer) SetupPrivateMatchRuleV1(ctx context.Context, request *api.PvpCasualSetupPrivateMatchRuleV1_Types_Request) (*api.PvpCasualSetupPrivateMatchRuleV1_Types_Response, error) {
	if _, err := currentPlayer(ctx); err != nil {
		return nil, err
	}
	password, err := privateMatchPassword(request.GetPassword())
	if err != nil {
		return nil, err
	}
	setting, created := s.rules.setup(password, request.GetRuleSetting())
	return &api.PvpCasualSetupPrivateMatchRuleV1_Types_Response{RuleSetting: setting, IsNew: created}, nil
}

// GetPrivateMatchRuleV1 returns no setting when the password has no live rule,
// which the client treats as an unrestricted room.
func (s pvpCasualServer) GetPrivateMatchRuleV1(ctx context.Context, request *api.PvpCasualGetPrivateMatchRuleV1_Types_Request) (*api.PvpCasualGetPrivateMatchRuleV1_Types_Response, error) {
	if _, err := currentPlayer(ctx); err != nil {
		return nil, err
	}
	password, err := privateMatchPassword(request.GetPassword())
	if err != nil {
		return nil, err
	}
	setting, _ := s.rules.get(password)
	return &api.PvpCasualGetPrivateMatchRuleV1_Types_Response{RuleSetting: setting}, nil
}

// The local server never completes versus battles, so both histories are empty.
func (pvpCasualServer) ListPublicMatchBattleRecordsV1(ctx context.Context, _ *api.PvpCasualListPublicMatchBattleRecordsV1_Types_Request) (*api.PvpCasualListPublicMatchBattleRecordsV1_Types_Response, error) {
	if _, err := currentPlayer(ctx); err != nil {
		return nil, err
	}
	return &api.PvpCasualListPublicMatchBattleRecordsV1_Types_Response{}, nil
}

func (pvpCasualServer) ListPrivateMatchBattleRecordsV1(ctx context.Context, _ *api.PvpCasualListPrivateMatchBattleRecordsV1_Types_Request) (*api.PvpCasualListPrivateMatchBattleRecordsV1_Types_Response, error) {
	if _, err := currentPlayer(ctx); err != nil {
		return nil, err
	}
	return &api.PvpCasualListPrivateMatchBattleRecordsV1_Types_Response{}, nil
}
