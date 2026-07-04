import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/history.dart';

const _fullProjectJson = '''
{
  "project_dir": "/Users/user/.claude/projects/-Users-user-project",
  "cwd": "/Users/user/project",
  "repo": "my-repo",
  "label": "my-repo",
  "session_count": 42,
  "last_activity": "2026-06-21T10:00:00Z",
  "node_id": "macbook",
  "node_label": "MacBook"
}
''';

const _minimalProjectJson = '''
{
  "project_dir": "/Users/user/.claude/projects/-Users-user-project",
  "cwd": "/Users/user/project",
  "label": "project",
  "session_count": 1,
  "last_activity": "2026-06-20T08:00:00Z"
}
''';

const _fullSessionJson = '''
{
  "session_id": "abc123",
  "title": "Fix the bug",
  "first_message": "Can you fix this bug?",
  "transcript_path": "/Users/user/.claude/projects/-Users-user-project/abc123.jsonl",
  "model_name": "Opus 4",
  "model_color": "#d3869b",
  "last_activity": "2026-06-21T10:00:00Z",
  "tokens": 5000,
  "turn_count": 10,
  "duration_ms": 120000,
  "node_id": "macbook",
  "node_label": "MacBook"
}
''';

const _minimalSessionJson = '''
{
  "session_id": "xyz789",
  "transcript_path": "/Users/user/.claude/projects/-Users-user-project/xyz789.jsonl",
  "last_activity": "2026-06-20T08:00:00Z"
}
''';

const _pageWithItemsJson = '''
{
  "items": [
    {
      "session_id": "abc123",
      "transcript_path": "/path/abc123.jsonl",
      "last_activity": "2026-06-21T10:00:00Z"
    },
    {
      "session_id": "def456",
      "transcript_path": "/path/def456.jsonl",
      "last_activity": "2026-06-20T10:00:00Z"
    }
  ],
  "has_more": true
}
''';

const _emptyPageJson = '''
{
  "items": [],
  "has_more": false
}
''';

void main() {
  group('HistoryProject.fromJson', () {
    test('parses a full project', () {
      final p = HistoryProject.fromJson(jsonDecode(_fullProjectJson));
      expect(p.projectDir,
          '/Users/user/.claude/projects/-Users-user-project');
      expect(p.cwd, '/Users/user/project');
      expect(p.repo, 'my-repo');
      expect(p.label, 'my-repo');
      expect(p.sessionCount, 42);
      expect(p.lastActivity, '2026-06-21T10:00:00Z');
      expect(p.nodeId, 'macbook');
      expect(p.nodeLabel, 'MacBook');
    });

    test('parses a project with optionals omitted', () {
      final p = HistoryProject.fromJson(jsonDecode(_minimalProjectJson));
      expect(p.projectDir,
          '/Users/user/.claude/projects/-Users-user-project');
      expect(p.cwd, '/Users/user/project');
      expect(p.repo, isNull);
      expect(p.label, 'project');
      expect(p.sessionCount, 1);
      expect(p.lastActivity, '2026-06-20T08:00:00Z');
      expect(p.nodeId, isNull);
      expect(p.nodeLabel, isNull);
    });
  });

  group('HistorySession.fromJson', () {
    test('parses a full session', () {
      final s = HistorySession.fromJson(jsonDecode(_fullSessionJson));
      expect(s.sessionId, 'abc123');
      expect(s.title, 'Fix the bug');
      expect(s.firstMessage, 'Can you fix this bug?');
      expect(s.transcriptPath,
          '/Users/user/.claude/projects/-Users-user-project/abc123.jsonl');
      expect(s.modelName, 'Opus 4');
      expect(s.modelColor, '#d3869b');
      expect(s.lastActivity, '2026-06-21T10:00:00Z');
      expect(s.tokens, 5000);
      expect(s.turnCount, 10);
      expect(s.durationMs, 120000);
      expect(s.nodeId, 'macbook');
      expect(s.nodeLabel, 'MacBook');
    });

    test('parses a session with optionals omitted', () {
      final s = HistorySession.fromJson(jsonDecode(_minimalSessionJson));
      expect(s.sessionId, 'xyz789');
      expect(s.title, isNull);
      expect(s.firstMessage, isNull);
      expect(s.transcriptPath,
          '/Users/user/.claude/projects/-Users-user-project/xyz789.jsonl');
      expect(s.modelName, isNull);
      expect(s.lastActivity, '2026-06-20T08:00:00Z');
      expect(s.tokens, 0);
      expect(s.turnCount, 0);
      expect(s.durationMs, 0);
      expect(s.nodeId, isNull);
      expect(s.nodeLabel, isNull);
    });
  });

  group('HistorySessionPage.fromJson', () {
    test('parses a page with items and has_more true', () {
      final page = HistorySessionPage.fromJson(jsonDecode(_pageWithItemsJson));
      expect(page.items, hasLength(2));
      expect(page.items[0].sessionId, 'abc123');
      expect(page.items[1].sessionId, 'def456');
      expect(page.hasMore, isTrue);
    });

    test('parses an empty page', () {
      final page = HistorySessionPage.fromJson(jsonDecode(_emptyPageJson));
      expect(page.items, isEmpty);
      expect(page.hasMore, isFalse);
    });
  });
}
