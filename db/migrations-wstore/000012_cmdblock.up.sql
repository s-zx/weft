-- Weft command-block tracking.
--
-- Each row is one "command" executed in a terminal block: from the moment the
-- shell integration emits OSC 16162;A (prompt start) to OSC 16162;D (command
-- done).  The raw output bytes are NOT duplicated here — we only record offsets
-- into the parent block's existing blockfile so the frontend can render a
-- per-command view by replaying slices of that file.
CREATE TABLE db_cmdblock (
    oid TEXT PRIMARY KEY,
    blockid TEXT NOT NULL,
    seq INTEGER NOT NULL,
    state TEXT NOT NULL,
    cmd TEXT,
    cwd TEXT,
    shell_type TEXT,
    exit_code INTEGER,
    duration_ms INTEGER,
    prompt_offset INTEGER NOT NULL,
    cmd_offset INTEGER,
    output_start_offset INTEGER,
    output_end_offset INTEGER,
    ts_prompt_ns INTEGER NOT NULL,
    ts_cmd_ns INTEGER,
    ts_done_ns INTEGER,
    agent_session_id TEXT,
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_cmdblock_blockid_seq ON db_cmdblock(blockid, seq);
CREATE INDEX idx_cmdblock_state ON db_cmdblock(state);
