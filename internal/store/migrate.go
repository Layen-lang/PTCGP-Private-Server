package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/gamelocale"
)

const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
  device_account TEXT PRIMARY KEY,
  password_hash BLOB,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  last_authorized_at INTEGER,
  authorized_player_id TEXT
);
CREATE TABLE IF NOT EXISTS players (
  player_id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  level INTEGER NOT NULL CHECK(level >= 1),
  experience INTEGER NOT NULL CHECK(experience >= 0),
  state_version INTEGER NOT NULL DEFAULT 1,
  nickname_changed_at INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS device_players (
  device_account TEXT NOT NULL REFERENCES devices(device_account) ON DELETE CASCADE,
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  linked_at INTEGER NOT NULL,
  PRIMARY KEY(device_account,player_id)
);
CREATE TABLE IF NOT EXISTS pending_profile (
  singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS active_profiles (
  device_account TEXT PRIMARY KEY REFERENCES devices(device_account) ON DELETE CASCADE,
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  selected_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS player_currencies (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  currency_id TEXT NOT NULL,
  quantity INTEGER NOT NULL CHECK(quantity >= 0),
  PRIMARY KEY(player_id,currency_id)
);
CREATE TABLE IF NOT EXISTS player_cards (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  card_id TEXT NOT NULL,
  quantity INTEGER NOT NULL CHECK(quantity >= 0),
  first_received_at INTEGER NOT NULL,
  last_received_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,card_id)
);
CREATE TABLE IF NOT EXISTS player_card_languages (
  player_id TEXT NOT NULL,
  card_id TEXT NOT NULL,
  language INTEGER NOT NULL CHECK(language BETWEEN 1 AND 12),
  quantity INTEGER NOT NULL CHECK(quantity >= 0),
  PRIMARY KEY(player_id,card_id,language),
  FOREIGN KEY(player_id,card_id) REFERENCES player_cards(player_id,card_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS player_items (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  item_kind TEXT NOT NULL,
  item_id TEXT NOT NULL,
  quantity INTEGER NOT NULL CHECK(quantity >= 0),
  obtained_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,item_kind,item_id)
);
CREATE TABLE IF NOT EXISTS player_profile_decorations (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  decoration_id TEXT NOT NULL,
  quantity INTEGER NOT NULL CHECK(quantity >= 0),
  obtained_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,decoration_id)
);
CREATE TABLE IF NOT EXISTS player_selected_emblems (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  display_order INTEGER NOT NULL,
  emblem_id TEXT NOT NULL,
  PRIMARY KEY(player_id,display_order),
  UNIQUE(player_id,emblem_id)
);
CREATE TABLE IF NOT EXISTS player_level_histories (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  player_level INTEGER NOT NULL,
  leveled_up_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,player_level)
);
CREATE TABLE IF NOT EXISTS player_action_states (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  action_key INTEGER NOT NULL,
  action_target TEXT NOT NULL DEFAULT '',
  action_date TEXT NOT NULL DEFAULT '',
  count INTEGER NOT NULL CHECK(count >= 0),
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,action_key,action_target,action_date)
);
CREATE INDEX IF NOT EXISTS player_action_states_sync ON player_action_states(player_id,updated_at,action_key,action_target,action_date);
CREATE TABLE IF NOT EXISTS player_card_skins (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  card_id TEXT NOT NULL,
  skin_id TEXT NOT NULL,
  quantity INTEGER NOT NULL CHECK(quantity >= 0),
  PRIMARY KEY(player_id,card_id,skin_id)
);
CREATE TABLE IF NOT EXISTS player_card_frames (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  card_id TEXT NOT NULL,
  frame_id TEXT NOT NULL,
  quantity INTEGER NOT NULL CHECK(quantity >= 0),
  PRIMARY KEY(player_id,card_id,frame_id)
);
CREATE TABLE IF NOT EXISTS player_settings (
  player_id TEXT PRIMARY KEY REFERENCES players(player_id) ON DELETE CASCADE,
  language TEXT NOT NULL,
  country TEXT NOT NULL,
  year_of_birth INTEGER NOT NULL,
  month_of_birth INTEGER NOT NULL,
  icon_id TEXT NOT NULL,
  message_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tutorial_progress (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  tutorial_id TEXT NOT NULL,
  step INTEGER NOT NULL,
  completed INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,tutorial_id)
);
CREATE TABLE IF NOT EXISTS player_storage (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  value BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,key)
);
CREATE TABLE IF NOT EXISTS player_flags (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  namespace TEXT NOT NULL,
  flag_key TEXT NOT NULL,
  flag_value INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,namespace,flag_key)
);
CREATE TABLE IF NOT EXISTS player_account_links (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  link_type INTEGER NOT NULL,
  linked_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,link_type)
);
CREATE TABLE IF NOT EXISTS player_showcases (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  showcase_kind TEXT NOT NULL,
  showcase_id INTEGER NOT NULL,
  display_order INTEGER NOT NULL,
  public_setting INTEGER NOT NULL,
  payload BLOB NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,showcase_kind,showcase_id)
);
CREATE INDEX IF NOT EXISTS player_showcases_order ON player_showcases(player_id,showcase_kind,display_order,showcase_id);
CREATE TABLE IF NOT EXISTS player_best_collections (
  player_id TEXT PRIMARY KEY REFERENCES players(player_id) ON DELETE CASCADE,
  payload BLOB NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS collection_likes (
  liker_player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  target_player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  liked_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  PRIMARY KEY(liker_player_id,target_player_id,liked_at)
);
CREATE INDEX IF NOT EXISTS collection_likes_target ON collection_likes(target_player_id,liked_at);
CREATE TABLE IF NOT EXISTS player_presents (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  present_id TEXT NOT NULL,
  payload BLOB NOT NULL,
  award_kind TEXT NOT NULL,
  award_id TEXT NOT NULL DEFAULT '',
  award_sub_id TEXT NOT NULL DEFAULT '',
  award_amount INTEGER NOT NULL DEFAULT 0 CHECK(award_amount >= 0),
  created_at INTEGER NOT NULL,
  expires_at INTEGER,
  viewed_at INTEGER,
  received_at INTEGER,
  PRIMARY KEY(player_id,present_id)
);
CREATE INDEX IF NOT EXISTS player_presents_pending ON player_presents(player_id,received_at,created_at,present_id);
CREATE TABLE IF NOT EXISTS card_exchange_counts (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  catalog_id TEXT NOT NULL,
  exchange_count INTEGER NOT NULL CHECK(exchange_count >= 0),
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,catalog_id)
);
CREATE TABLE IF NOT EXISTS sessions (
  token_hash BLOB PRIMARY KEY,
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  device_account TEXT NOT NULL REFERENCES devices(device_account) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  revoked_at INTEGER
);
CREATE INDEX IF NOT EXISTS sessions_player_id ON sessions(player_id);
CREATE INDEX IF NOT EXISTS sessions_device_account ON sessions(device_account);
CREATE TABLE IF NOT EXISTS idempotency_keys (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  method TEXT NOT NULL,
  key TEXT NOT NULL,
  response BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,method,key)
);
CREATE TABLE IF NOT EXISTS player_pack_rules (
  player_id TEXT PRIMARY KEY REFERENCES players(player_id) ON DELETE CASCADE,
  mode TEXT NOT NULL,
  target_pack_id TEXT NOT NULL DEFAULT '',
  table_kind TEXT NOT NULL DEFAULT '',
  rarity TEXT NOT NULL DEFAULT '',
  return_pack_count INTEGER NOT NULL DEFAULT 1 CHECK(return_pack_count >= 0),
  illegal_card_count INTEGER NOT NULL DEFAULT 5 CHECK(illegal_card_count >= 0),
  illegal_allow_duplicates INTEGER NOT NULL DEFAULT 0,
  illegal_expansion_ids TEXT NOT NULL DEFAULT '[]',
  illegal_rarities TEXT NOT NULL DEFAULT '[]',
  illegal_card_kinds TEXT NOT NULL DEFAULT '[]',
  free_openings INTEGER NOT NULL DEFAULT 0,
  fixed_seed INTEGER,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS player_pack_rule_packs (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  pack_index INTEGER NOT NULL CHECK(pack_index >= 0),
  PRIMARY KEY(player_id,pack_index)
);
CREATE TABLE IF NOT EXISTS player_pack_rule_slots (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  pack_index INTEGER NOT NULL,
  slot_index INTEGER NOT NULL CHECK(slot_index >= 0),
  card_id TEXT NOT NULL,
  locked INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY(player_id,pack_index,slot_index),
  FOREIGN KEY(player_id,pack_index) REFERENCES player_pack_rule_packs(player_id,pack_index) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS global_pack_rule (
  singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
  mode TEXT NOT NULL,
  target_pack_id TEXT NOT NULL DEFAULT '',
  table_kind TEXT NOT NULL DEFAULT '',
  rarity TEXT NOT NULL DEFAULT '',
  return_pack_count INTEGER NOT NULL DEFAULT 1 CHECK(return_pack_count >= 0),
  illegal_card_count INTEGER NOT NULL DEFAULT 5 CHECK(illegal_card_count >= 0),
  illegal_allow_duplicates INTEGER NOT NULL DEFAULT 0,
  illegal_expansion_ids TEXT NOT NULL DEFAULT '[]',
  illegal_rarities TEXT NOT NULL DEFAULT '[]',
  illegal_card_kinds TEXT NOT NULL DEFAULT '[]',
  free_openings INTEGER NOT NULL DEFAULT 0,
  fixed_seed INTEGER,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS global_pack_rule_packs (
  pack_index INTEGER PRIMARY KEY CHECK(pack_index >= 0)
);
CREATE TABLE IF NOT EXISTS global_pack_rule_slots (
  pack_index INTEGER NOT NULL,
  slot_index INTEGER NOT NULL CHECK(slot_index >= 0),
  card_id TEXT NOT NULL,
  locked INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY(pack_index,slot_index),
  FOREIGN KEY(pack_index) REFERENCES global_pack_rule_packs(pack_index) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS player_pack_state (
  player_id TEXT PRIMARY KEY REFERENCES players(player_id) ON DELETE CASCADE,
  pack_power INTEGER NOT NULL DEFAULT 2 CHECK(pack_power >= 0),
  pack_power_updated_at INTEGER NOT NULL,
  poke_gold INTEGER NOT NULL DEFAULT 0 CHECK(poke_gold >= 0)
);
CREATE TABLE IF NOT EXISTS player_pack_ceil_points (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  group_id TEXT NOT NULL,
  quantity INTEGER NOT NULL CHECK(quantity >= 0),
  PRIMARY KEY(player_id,group_id)
);
CREATE TABLE IF NOT EXISTS pack_shop_exchanges (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  transaction_id TEXT NOT NULL,
  group_id TEXT NOT NULL,
  card_id TEXT NOT NULL,
  amount INTEGER NOT NULL,
  cost INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,transaction_id)
);
CREATE TABLE IF NOT EXISTS item_shop_transactions (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  transaction_id TEXT NOT NULL,
  shop_id TEXT NOT NULL,
  product_id TEXT NOT NULL,
  amount INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,transaction_id)
);
CREATE INDEX IF NOT EXISTS item_shop_product_count ON item_shop_transactions(player_id,shop_id,product_id);
CREATE TABLE IF NOT EXISTS player_trophies (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  trophy_id TEXT NOT NULL,
  trophy_rank INTEGER NOT NULL CHECK(trophy_rank BETWEEN 1 AND 4),
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,trophy_id)
);
CREATE TABLE IF NOT EXISTS pack_openings (
  opening_id TEXT PRIMARY KEY,
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  transaction_id TEXT NOT NULL,
  product_id TEXT NOT NULL,
  pack_id TEXT NOT NULL,
  requested_count INTEGER NOT NULL,
  returned_count INTEGER NOT NULL,
  mode TEXT NOT NULL,
  seed INTEGER NOT NULL,
  free_opening INTEGER NOT NULL,
  power_cost INTEGER NOT NULL,
  experience_reward INTEGER NOT NULL,
  ceil_point_reward INTEGER NOT NULL,
  shine_dust_reward INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE(player_id,transaction_id)
);
CREATE TABLE IF NOT EXISTS pack_opening_packs (
  opening_id TEXT NOT NULL REFERENCES pack_openings(opening_id) ON DELETE CASCADE,
  pack_index INTEGER NOT NULL,
  pack_table_id TEXT NOT NULL,
  PRIMARY KEY(opening_id,pack_index)
);
CREATE TABLE IF NOT EXISTS pack_opening_cards (
  opening_id TEXT NOT NULL,
  pack_index INTEGER NOT NULL,
  slot_index INTEGER NOT NULL,
  card_id TEXT NOT NULL,
  PRIMARY KEY(opening_id,pack_index,slot_index),
  FOREIGN KEY(opening_id,pack_index) REFERENCES pack_opening_packs(opening_id,pack_index) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS player_mission_counters (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  quantity INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(player_id,event_type)
);
CREATE TABLE IF NOT EXISTS player_missions (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  mission_id TEXT NOT NULL,
  completed_at INTEGER,
  claimed_at INTEGER,
  PRIMARY KEY(player_id,mission_id)
);
CREATE TABLE IF NOT EXISTS player_decks (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  deck_id TEXT NOT NULL,
  name TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,deck_id)
);
CREATE TABLE IF NOT EXISTS player_deck_cards (
  player_id TEXT NOT NULL,
  deck_id TEXT NOT NULL,
  card_id TEXT NOT NULL,
  quantity INTEGER NOT NULL CHECK(quantity > 0),
  PRIMARY KEY(player_id,deck_id,card_id),
  FOREIGN KEY(player_id,deck_id) REFERENCES player_decks(player_id,deck_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS friend_requests (
  sender_player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  receiver_player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY(sender_player_id,receiver_player_id),
  CHECK(sender_player_id <> receiver_player_id)
);
CREATE TABLE IF NOT EXISTS friendships (
  player_low_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  player_high_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY(player_low_id,player_high_id),
  CHECK(player_low_id < player_high_id)
);
CREATE TABLE IF NOT EXISTS friend_favorites (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  friend_player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  PRIMARY KEY(player_id,friend_player_id)
);
CREATE TABLE IF NOT EXISTS feed_entries (
  entry_id TEXT PRIMARY KEY,
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  opening_id TEXT REFERENCES pack_openings(opening_id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS feed_entries_opening_unique ON feed_entries(opening_id) WHERE opening_id IS NOT NULL;
CREATE TABLE IF NOT EXISTS player_challenge_state (
  player_id TEXT PRIMARY KEY REFERENCES players(player_id) ON DELETE CASCADE,
  power INTEGER NOT NULL CHECK(power >= 0),
  power_updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS feed_snoops (
  player_id TEXT PRIMARY KEY REFERENCES players(player_id) ON DELETE CASCADE,
  feed_id TEXT NOT NULL REFERENCES feed_entries(entry_id) ON DELETE CASCADE,
  cost INTEGER NOT NULL CHECK(cost > 0),
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS feed_challenges (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  feed_id TEXT NOT NULL REFERENCES feed_entries(entry_id) ON DELETE CASCADE,
  transaction_id TEXT NOT NULL,
  card_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,feed_id),
  UNIQUE(player_id,transaction_id)
);
CREATE TABLE IF NOT EXISTS solo_battle_runs (
  run_id TEXT PRIMARY KEY,
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  battle_id TEXT NOT NULL,
  started_at INTEGER NOT NULL,
  finished_at INTEGER,
  result INTEGER,
  rewarded INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS solo_battle_try_progress (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  battle_id TEXT NOT NULL,
  battle_try_id TEXT NOT NULL,
  current_count INTEGER NOT NULL DEFAULT 0 CHECK(current_count >= 0),
  reward_received INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,battle_id,battle_try_id)
);
CREATE TABLE IF NOT EXISTS player_rental_decks (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  rental_deck_id TEXT NOT NULL,
  used_count INTEGER NOT NULL DEFAULT 0 CHECK(used_count >= 0),
  obtained_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,rental_deck_id)
);
CREATE TABLE IF NOT EXISTS player_theme_deck_recipes (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  theme_deck_recipe_id TEXT NOT NULL,
  obtained_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,theme_deck_recipe_id)
);
CREATE TABLE IF NOT EXISTS player_mission_group_steps (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  mission_group_reward_step_id TEXT NOT NULL,
  claimed_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,mission_group_reward_step_id)
);
CREATE TABLE IF NOT EXISTS player_event_powers (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  event_id TEXT NOT NULL,
  power INTEGER NOT NULL CHECK(power >= 0),
  power_updated_at INTEGER NOT NULL,
  PRIMARY KEY(player_id,event_id)
);
CREATE TABLE IF NOT EXISTS player_desired_cards (
  player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  card_id TEXT NOT NULL,
  display_order INTEGER NOT NULL CHECK(display_order >= 0),
  profile_order INTEGER NOT NULL CHECK(profile_order >= 0),
  PRIMARY KEY(player_id,card_id)
);
CREATE TABLE IF NOT EXISTS give_card_histories (
  history_id INTEGER PRIMARY KEY AUTOINCREMENT,
  sender_player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  receiver_player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  card_id TEXT NOT NULL,
  expansion_id TEXT NOT NULL,
  language INTEGER NOT NULL,
  sender_remaining_amount INTEGER NOT NULL CHECK(sender_remaining_amount >= 0),
  given_at INTEGER NOT NULL,
  CHECK(sender_player_id <> receiver_player_id)
);
CREATE INDEX IF NOT EXISTS give_card_histories_sender_time ON give_card_histories(sender_player_id,given_at);
CREATE INDEX IF NOT EXISTS give_card_histories_receiver_time ON give_card_histories(receiver_player_id,given_at);
CREATE TABLE IF NOT EXISTS thank_rewards (
  sender_player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  target_player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  route_type INTEGER NOT NULL,
  sent_at INTEGER NOT NULL,
  sent_day INTEGER NOT NULL,
  PRIMARY KEY(sender_player_id,target_player_id,route_type,sent_day)
);
CREATE TABLE IF NOT EXISTS player_trade_state (
  player_id TEXT PRIMARY KEY REFERENCES players(player_id) ON DELETE CASCADE,
  power INTEGER NOT NULL DEFAULT 5 CHECK(power >= 0),
  power_updated_at INTEGER NOT NULL,
  block_all INTEGER NOT NULL DEFAULT 0,
  message_stance_id TEXT NOT NULL DEFAULT 'STANCE_ID_DESIRED_ONLY',
  message_language INTEGER NOT NULL DEFAULT 4
);
CREATE TABLE IF NOT EXISTS trade_sessions (
  session_id TEXT PRIMARY KEY,
  proposer_player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  partner_player_id TEXT NOT NULL REFERENCES players(player_id) ON DELETE CASCADE,
  proposer_card_id TEXT NOT NULL,
  proposer_expansion_id TEXT NOT NULL,
  proposer_language INTEGER NOT NULL,
  proposer_card_last_amount INTEGER NOT NULL,
  proposer_deposit BLOB,
  proposer_message_stance_id TEXT NOT NULL DEFAULT '',
  proposer_message_language INTEGER NOT NULL DEFAULT 0,
  partner_card_id TEXT,
  partner_expansion_id TEXT,
  partner_language INTEGER,
  partner_card_last_amount INTEGER,
  partner_deposit BLOB,
  state INTEGER NOT NULL,
  proposer_received INTEGER NOT NULL DEFAULT 0,
  partner_received INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  expire_at INTEGER NOT NULL,
  completed_at INTEGER,
  CHECK(proposer_player_id <> partner_player_id)
);
CREATE INDEX IF NOT EXISTS trade_sessions_proposer_state ON trade_sessions(proposer_player_id,state,updated_at);
CREATE INDEX IF NOT EXISTS trade_sessions_partner_state ON trade_sessions(partner_player_id,state,updated_at);
`

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`); err != nil {
		return fmt.Errorf("configure sqlite: %w", err)
	}
	legacy, err := hasColumn(ctx, s.db, "players", "device_account")
	if err != nil {
		return err
	}
	if legacy {
		if err := s.migrateLegacy(ctx); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	for _, column := range []struct{ name, kind string }{{"password_hash", "BLOB"}, {"last_authorized_at", "INTEGER"}, {"authorized_player_id", "TEXT"}} {
		if err := ensureColumn(ctx, s.db, "devices", column.name, column.kind); err != nil {
			return err
		}
	}
	for _, column := range []struct{ name, kind string }{
		{"comeback_absence_days", "INTEGER NOT NULL DEFAULT 0"},
		{"comeback_returned_at", "INTEGER"},
		{"last_comeback_at", "INTEGER"},
	} {
		if err := ensureColumn(ctx, s.db, "devices", column.name, column.kind); err != nil {
			return err
		}
	}
	if err := ensureColumn(ctx, s.db, "players", "display_order", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, s.db, "players", "nickname_changed_at", "INTEGER"); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `WITH RECURSIVE levels(player_id,player_level,max_level,leveled_up_at) AS (
		SELECT player_id,1,level,created_at FROM players WHERE level>=1
		UNION ALL
		SELECT player_id,player_level+1,max_level,leveled_up_at FROM levels WHERE player_level<max_level
	) INSERT OR IGNORE INTO player_level_histories(player_id,player_level,leveled_up_at) SELECT player_id,player_level,leveled_up_at FROM levels`); err != nil {
		return fmt.Errorf("backfill level histories: %w", err)
	}
	for _, column := range []struct{ name, kind string }{
		{"display_order", "INTEGER NOT NULL DEFAULT 0"}, {"energy_types", "TEXT NOT NULL DEFAULT ''"},
		{"deck_case_type", "INTEGER NOT NULL DEFAULT 0"}, {"deck_shield_id", "TEXT NOT NULL DEFAULT ''"},
		{"coin_skin_id", "TEXT NOT NULL DEFAULT ''"}, {"play_mat_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureColumn(ctx, s.db, "player_decks", column.name, column.kind); err != nil {
			return err
		}
	}
	if err := s.applyPackLabMigration(ctx); err != nil {
		return err
	}
	if err := s.applyGlobalPackRuleMigration(ctx); err != nil {
		return err
	}
	if err := s.applyCardLanguageMigration(ctx); err != nil {
		return err
	}
	for _, table := range []string{"player_pack_rules", "global_pack_rule"} {
		for _, column := range []struct{ name, kind string }{
			{"illegal_card_count", "INTEGER NOT NULL DEFAULT 5"},
			{"illegal_allow_duplicates", "INTEGER NOT NULL DEFAULT 0"},
			{"illegal_expansion_ids", "TEXT NOT NULL DEFAULT '[]'"},
			{"illegal_rarities", "TEXT NOT NULL DEFAULT '[]'"},
			{"illegal_card_kinds", "TEXT NOT NULL DEFAULT '[]'"},
		} {
			if err := ensureColumn(ctx, s.db, table, column.name, column.kind); err != nil {
				return err
			}
		}
	}
	return s.applyTutorialCompletionMigration(ctx)
}

func (s *Store) applyCardLanguageMigration(ctx context.Context) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=6`).Scan(&exists); err != nil {
		return fmt.Errorf("check card language migration: %w", err)
	}
	if exists != 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin card language migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO player_card_languages(player_id,card_id,language,quantity) SELECT player_id,card_id,4,quantity FROM player_cards WHERE quantity>0`); err != nil {
		return fmt.Errorf("backfill French card quantities: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(6,?)`, s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("record card language migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit card language migration: %w", err)
	}
	return nil
}

func (s *Store) applyTutorialCompletionMigration(ctx context.Context) error {
	if err := s.backfillCompletedTutorials(ctx, 4, previousCompletedTutorial, tutorialCompletionV4); err != nil {
		return err
	}
	return s.backfillCompletedTutorials(ctx, 5, tutorialCompletionV4, completedTutorial)
}

func (s *Store) backfillCompletedTutorials(ctx context.Context, version int, required, target []TutorialStep) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=?`, version).Scan(&exists); err != nil {
		return fmt.Errorf("check tutorial completion migration %d: %w", version, err)
	}
	if exists != 0 {
		return nil
	}
	playerIDs, err := s.playersWithCompletedTutorial(ctx, required)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tutorial completion migration %d: %w", version, err)
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	for _, playerID := range playerIDs {
		for _, step := range target {
			_, err := tx.ExecContext(ctx, `
INSERT INTO tutorial_progress(player_id,tutorial_id,step,completed,updated_at)
VALUES(?,?,?,?,?)
ON CONFLICT(player_id,tutorial_id) DO UPDATE SET
  step=MAX(tutorial_progress.step,excluded.step),
	  completed=1,
	  updated_at=excluded.updated_at`, playerID, step.TutorialID, step.Step, true, now)
			if err != nil {
				return fmt.Errorf("backfill tutorial %q for player %q in migration %d: %w", step.TutorialID, playerID, version, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, version, now); err != nil {
		return fmt.Errorf("record tutorial completion migration %d: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tutorial completion migration %d: %w", version, err)
	}
	return nil
}

func (s *Store) playersWithCompletedTutorial(ctx context.Context, required []TutorialStep) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT player_id,tutorial_id,step,completed FROM tutorial_progress ORDER BY player_id,tutorial_id`)
	if err != nil {
		return nil, fmt.Errorf("load tutorial migration candidates: %w", err)
	}
	defer rows.Close()
	progress := map[string][]TutorialStep{}
	for rows.Next() {
		var playerID string
		var value TutorialStep
		if err := rows.Scan(&playerID, &value.TutorialID, &value.Step, &value.Completed); err != nil {
			return nil, fmt.Errorf("scan tutorial migration candidate: %w", err)
		}
		progress[playerID] = append(progress[playerID], value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tutorial migration candidates: %w", err)
	}
	var result []string
	for playerID, values := range progress {
		if hasCompletedTutorial(values, required) {
			result = append(result, playerID)
		}
	}
	return result, nil
}

func (s *Store) applyGlobalPackRuleMigration(ctx context.Context) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=3`).Scan(&exists); err != nil {
		return fmt.Errorf("check global pack rule migration: %w", err)
	}
	if exists != 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin global pack rule migration: %w", err)
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	var playerID string
	err = tx.QueryRowContext(ctx, `SELECT player_id FROM player_pack_rules ORDER BY updated_at DESC LIMIT 1`).Scan(&playerID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO global_pack_rule(singleton,mode,return_pack_count,updated_at) VALUES(1,'official',1,?)`, now)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO global_pack_rule(singleton,mode,target_pack_id,table_kind,rarity,return_pack_count,free_openings,fixed_seed,updated_at) SELECT 1,mode,target_pack_id,table_kind,rarity,return_pack_count,free_openings,fixed_seed,updated_at FROM player_pack_rules WHERE player_id=?`, playerID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO global_pack_rule_packs(pack_index) SELECT pack_index FROM player_pack_rule_packs WHERE player_id=?`, playerID)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO global_pack_rule_slots(pack_index,slot_index,card_id,locked) SELECT pack_index,slot_index,card_id,locked FROM player_pack_rule_slots WHERE player_id=?`, playerID)
		}
	}
	if err != nil {
		return fmt.Errorf("initialize global pack rule: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(3,?)`, now); err != nil {
		return fmt.Errorf("record global pack rule migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit global pack rule migration: %w", err)
	}
	return nil
}

func (s *Store) applyPackLabMigration(ctx context.Context) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=2`).Scan(&exists); err != nil {
		return fmt.Errorf("check pack lab migration: %w", err)
	}
	if exists != 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pack lab migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM tutorial_progress`); err != nil {
		return fmt.Errorf("reset tutorial migration: %w", err)
	}
	now := s.now().UTC().Unix()
	for _, step := range completedTutorial {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tutorial_progress(player_id,tutorial_id,step,completed,updated_at) SELECT player_id,?,?,1,? FROM players`, step.TutorialID, step.Step, now); err != nil {
			return fmt.Errorf("complete tutorial migration: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO player_pack_state(player_id,pack_power,pack_power_updated_at,poke_gold) SELECT player_id,2,?,0 FROM players`, now); err != nil {
		return fmt.Errorf("initialize pack state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(2,?)`, now); err != nil {
		return fmt.Errorf("record pack lab migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pack lab migration: %w", err)
	}
	return nil
}

func (s *Store) migrateLegacy(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer s.db.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy migration: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`ALTER TABLE sessions RENAME TO sessions_legacy`,
		`ALTER TABLE idempotency_keys RENAME TO idempotency_keys_legacy`,
		`ALTER TABLE players RENAME TO players_legacy`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prepare legacy migration: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create multi-profile schema: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT player_id,device_account,display_name,state_version,state_json,created_at FROM players_legacy ORDER BY created_at`)
	if err != nil {
		return err
	}
	type legacyPlayer struct {
		id, device, name string
		version, created int64
		state            string
	}
	var players []legacyPlayer
	for rows.Next() {
		var p legacyPlayer
		if err := rows.Scan(&p.id, &p.device, &p.name, &p.version, &p.state, &p.created); err != nil {
			rows.Close()
			return err
		}
		players = append(players, p)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, old := range players {
		var state struct {
			TutorialComplete bool  `json:"tutorial_complete"`
			Level            int   `json:"level"`
			Experience       int64 `json:"experience"`
		}
		_ = json.Unmarshal([]byte(old.state), &state)
		if state.Level < 1 {
			state.Level = 1
		}
		created := time.Unix(old.created, 0).UTC()
		p := Player{ID: old.id, DisplayName: old.name, Level: state.Level, Experience: state.Experience, StateVersion: old.version, CreatedAt: created, UpdatedAt: created}
		if err := insertPlayer(ctx, tx, p, state.TutorialComplete, gamelocale.DefaultLocale, gamelocale.DefaultCountry); err != nil {
			return fmt.Errorf("migrate legacy player %s: %w", old.id, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO device_players(device_account,player_id,linked_at) VALUES(?,?,?)`, old.device, old.id, old.created); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO active_profiles(device_account,player_id,selected_at) VALUES(?,?,?)`, old.device, old.id, old.created); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(token_hash,player_id,device_account,created_at,expires_at,revoked_at) SELECT s.token_hash,s.player_id,p.device_account,s.created_at,s.expires_at,s.revoked_at FROM sessions_legacy s JOIN players_legacy p ON p.player_id=s.player_id`); err != nil {
		return fmt.Errorf("migrate sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys(player_id,method,key,response,created_at) SELECT player_id,method,key,response,created_at FROM idempotency_keys_legacy`); err != nil {
		return fmt.Errorf("migrate idempotency: %w", err)
	}
	for _, table := range []string{"sessions_legacy", "idempotency_keys_legacy", "players_legacy"} {
		if _, err := tx.ExecContext(ctx, `DROP TABLE `+table); err != nil {
			return fmt.Errorf("finish legacy migration: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy migration: %w", err)
	}
	return nil
}

func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func ensureColumn(ctx context.Context, db *sql.DB, table, column, kind string) error {
	ok, err := hasColumn(ctx, db, table, column)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	_, err = db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+kind)
	if err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
