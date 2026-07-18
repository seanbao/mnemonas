import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';

void main() {
  test('session serializes a rotating token pair as one versioned record', () {
    final session = AuthSession(
      serverBaseUrl: 'https://nas.example.com',
      tokens: AuthTokenPair(
        accessToken: 'access-next',
        refreshToken: 'refresh-next',
        expiresAt: DateTime.utc(2026, 7, 19, 12),
      ),
      lastRefreshAt: DateTime.utc(2026, 7, 19, 11, 30),
    );

    final encoded = session.toJson();
    final restored = AuthSession.fromJson(encoded);

    expect(encoded['schema_version'], 1);
    expect(restored.serverBaseUrl, session.serverBaseUrl);
    expect(restored.tokens.accessToken, 'access-next');
    expect(restored.tokens.refreshToken, 'refresh-next');
    expect(restored.tokens.expiresAt, session.tokens.expiresAt);
    expect(restored.lastRefreshAt, session.lastRefreshAt);
  });

  test('rejects incomplete token generations', () {
    expect(
      () => AuthSession.fromJson({
        'schema_version': 1,
        'server_base_url': 'https://nas.example.com',
        'tokens': {
          'access_token': 'access-only',
          'expires_at': '2026-07-19T12:00:00Z',
          'token_type': 'Bearer',
        },
      }),
      throwsFormatException,
    );
  });
}
