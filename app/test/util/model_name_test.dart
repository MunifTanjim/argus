import 'package:flutter_test/flutter_test.dart';
import 'package:argus/util/model_name.dart';

void main() {
  test('formats current model ids', () {
    expect(formatModelName('claude-opus-4-8'), 'Opus 4.8');
    expect(formatModelName('claude-sonnet-5'), 'Sonnet 5');
    expect(formatModelName('claude-haiku-4-5'), 'Haiku 4.5');
    expect(formatModelName('claude-fable-5'), 'Fable 5');
  });

  test('drops date stamps', () {
    expect(formatModelName('claude-opus-4-7-20260201'), 'Opus 4.7');
    expect(formatModelName('claude-sonnet-4-20250514'), 'Sonnet 4');
    expect(formatModelName('claude-sonnet-4-5-20250514'), 'Sonnet 4.5');
  });

  test('handles legacy version-first order', () {
    expect(formatModelName('claude-3-5-sonnet'), 'Sonnet 3.5');
    expect(formatModelName('claude-3-5-sonnet-20241022'), 'Sonnet 3.5');
  });

  test('preserves variant tag', () {
    expect(formatModelName('claude-opus-4-8[1m]'), 'Opus 4.8 [1m]');
  });

  test('passes through unknown or empty', () {
    expect(formatModelName('claude-code'), 'claude-code');
    expect(formatModelName('gpt-4o'), 'gpt-4o');
    expect(formatModelName(''), '');
  });
}
