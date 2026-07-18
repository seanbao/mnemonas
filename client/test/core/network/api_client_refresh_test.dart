import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/auth/auth_api.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';

void main() {
  final now = DateTime.utc(2026, 7, 19, 12);

  test('concurrent 401 responses share one rotating refresh request', () async {
    final store = MemoryAuthSessionStore();
    await _seedSession(
      store,
      _session(expiresAt: now.subtract(const Duration(minutes: 1))),
    );
    final adapter = _AuthAdapter(oldRequestsBeforeFailure: 2);
    final dio = Dio()..httpClientAdapter = adapter;
    final client = ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: dio,
      clock: () => now,
    );

    Future<String> requestValue() async {
      final response = await client.requestEnvelope<String>(
        '/api/v1/protected',
        decode: (data) => (data as Map<String, dynamic>)['value']! as String,
      );
      return response.data;
    }

    final values = await Future.wait([requestValue(), requestValue()]);

    expect(values, ['ok', 'ok']);
    expect(adapter.refreshRequests, 1);
    expect(adapter.refreshTokens, ['refresh-old']);
    expect(adapter.newAccessRequests, 2);
    final saved = (await store.snapshot()).session;
    expect(saved?.tokens.accessToken, 'access-new');
    expect(saved?.tokens.refreshToken, 'refresh-new');
    expect(saved?.lastRefreshAt, now);
  });

  test('local cooldown prevents a second rotation within 30 seconds', () async {
    final store = MemoryAuthSessionStore();
    await _seedSession(
      store,
      _session(
        expiresAt: now.add(const Duration(minutes: 1)),
        lastRefreshAt: now.subtract(const Duration(seconds: 5)),
      ),
    );
    final adapter = _AuthAdapter(oldRequestsBeforeFailure: 1);
    final dio = Dio()..httpClientAdapter = adapter;
    final client = ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: dio,
      clock: () => now,
    );

    await expectLater(
      client.requestEnvelope<String>(
        '/api/v1/protected',
        decode: (data) => (data as Map<String, dynamic>)['value']! as String,
      ),
      throwsA(
        isA<ApiException>()
            .having((error) => error.code, 'code', 'REFRESH_COOLDOWN')
            .having(
              (error) => error.retryAfter,
              'retryAfter',
              const Duration(seconds: 25),
            ),
      ),
    );
    expect(adapter.refreshRequests, 0);
  });

  test('revoked refresh token clears the persisted session', () async {
    final store = MemoryAuthSessionStore();
    await _seedSession(
      store,
      _session(expiresAt: now.subtract(const Duration(minutes: 1))),
    );
    final adapter = _AuthAdapter(
      oldRequestsBeforeFailure: 1,
      refreshFailureCode: 'TOKEN_REVOKED',
    );
    final dio = Dio()..httpClientAdapter = adapter;
    final client = ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: dio,
      clock: () => now,
    );

    await expectLater(
      client.requestEnvelope<String>(
        '/api/v1/protected',
        decode: (data) => (data as Map<String, dynamic>)['value']! as String,
      ),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'TOKEN_REVOKED',
        ),
      ),
    );
    expect((await store.snapshot()).session, isNull);
  });

  test('does not refresh non-expiry 401 responses', () async {
    final store = MemoryAuthSessionStore();
    await _seedSession(
      store,
      _session(expiresAt: now.add(const Duration(minutes: 1))),
    );
    final adapter = _AuthAdapter(
      oldRequestsBeforeFailure: 1,
      protectedFailureCode: 'TOKEN_REVOKED',
    );
    final dio = Dio()..httpClientAdapter = adapter;
    final client = ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: dio,
      clock: () => now,
    );

    await expectLater(
      client.requestEnvelope<String>(
        '/api/v1/protected',
        decode: (data) => (data as Map<String, dynamic>)['value']! as String,
      ),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'TOKEN_REVOKED',
        ),
      ),
    );
    expect(adapter.refreshRequests, 0);
  });

  test('a delayed refresh cannot restore a session after logout', () async {
    final store = MemoryAuthSessionStore();
    await _seedSession(
      store,
      _session(expiresAt: now.subtract(const Duration(minutes: 1))),
    );
    final adapter = _AuthAdapter(
      oldRequestsBeforeFailure: 0,
      holdRefresh: true,
    );
    addTearDown(adapter.releaseRefresh);
    final dio = Dio()..httpClientAdapter = adapter;
    final client = ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: dio,
      clock: () => now,
    );
    final api = AuthApi(client);

    final refresh = client.refreshSession();
    await adapter.refreshStarted.future.timeout(const Duration(seconds: 2));
    var localClearCount = 0;

    await expectLater(
      api.logout(
        onLocalSessionCleared: () {
          localClearCount++;
        },
      ),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'AUTH_SESSION_MISSING',
        ),
      ),
    );
    expect(localClearCount, 1);
    expect((await store.snapshot()).session, isNull);

    adapter.releaseRefresh();
    await expectLater(
      refresh,
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'AUTH_CONTEXT_CHANGED',
        ),
      ),
    );
    expect((await store.snapshot()).session, isNull);
    expect(adapter.refreshRequests, 1);
  });

  test('a closed client cannot commit a delayed refresh result', () async {
    final store = MemoryAuthSessionStore();
    await _seedSession(
      store,
      _session(expiresAt: now.subtract(const Duration(minutes: 1))),
    );
    final adapter = _AuthAdapter(
      oldRequestsBeforeFailure: 0,
      holdRefresh: true,
    );
    addTearDown(adapter.releaseRefresh);
    final dio = Dio()..httpClientAdapter = adapter;
    final client = ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: dio,
      clock: () => now,
    );

    final refresh = client.refreshSession();
    await adapter.refreshStarted.future.timeout(const Duration(seconds: 2));
    client.close();
    adapter.releaseRefresh();

    await expectLater(
      refresh,
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'AUTH_CONTEXT_CHANGED',
        ),
      ),
    );
    expect((await store.snapshot()).session, isNull);
  });

  test('a stale refresh failure does not clear a newer session', () async {
    final store = MemoryAuthSessionStore();
    await _seedSession(
      store,
      _session(expiresAt: now.subtract(const Duration(minutes: 1))),
    );
    final adapter = _AuthAdapter(
      oldRequestsBeforeFailure: 0,
      refreshFailureCode: 'TOKEN_REVOKED',
      holdRefresh: true,
    );
    addTearDown(adapter.releaseRefresh);
    final dio = Dio()..httpClientAdapter = adapter;
    final client = ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: dio,
      clock: () => now,
    );

    final refresh = client.refreshSession();
    await adapter.refreshStarted.future.timeout(const Duration(seconds: 2));
    final vacant = await store.snapshot();
    expect(vacant.session, isNull);
    final replacement = _session(
      expiresAt: now.add(const Duration(hours: 2)),
      accessToken: 'access-replacement',
      refreshToken: 'refresh-replacement',
    );
    expect(await store.commitIfRevision(vacant.revision, replacement), isTrue);

    adapter.releaseRefresh();
    await expectLater(
      refresh,
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'TOKEN_REVOKED',
        ),
      ),
    );
    final current = (await store.snapshot()).session;
    expect(current?.tokens.accessToken, 'access-replacement');
    expect(current?.tokens.refreshToken, 'refresh-replacement');
  });

  test('refresh commit storage failure remains fail-closed', () async {
    final store = _CommitFailingSessionStore(
      _session(expiresAt: now.subtract(const Duration(minutes: 1))),
    );
    final adapter = _AuthAdapter(oldRequestsBeforeFailure: 0);
    final dio = Dio()..httpClientAdapter = adapter;
    final client = ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: dio,
      clock: () => now,
    );

    await expectLater(
      client.refreshSession(),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'AUTH_SESSION_STORAGE_FAILED',
        ),
      ),
    );
    expect((await store.snapshot()).session, isNull);
    expect(adapter.refreshRequests, 1);

    await expectLater(
      client.refreshSession(),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'AUTH_SESSION_MISSING',
        ),
      ),
    );
    expect(adapter.refreshRequests, 1);
  });
}

Future<void> _seedSession(
  MemoryAuthSessionStore store,
  AuthSession session,
) async {
  final snapshot = await store.snapshot();
  expect(await store.commitIfRevision(snapshot.revision, session), isTrue);
}

AuthSession _session({
  required DateTime expiresAt,
  DateTime? lastRefreshAt,
  String accessToken = 'access-old',
  String refreshToken = 'refresh-old',
}) {
  return AuthSession(
    serverBaseUrl: 'https://nas.example.com',
    tokens: AuthTokenPair(
      accessToken: accessToken,
      refreshToken: refreshToken,
      expiresAt: expiresAt,
    ),
    lastRefreshAt: lastRefreshAt,
  );
}

final class _AuthAdapter implements HttpClientAdapter {
  _AuthAdapter({
    required this.oldRequestsBeforeFailure,
    this.refreshFailureCode,
    this.protectedFailureCode = 'TOKEN_EXPIRED',
    this.holdRefresh = false,
  });

  final int oldRequestsBeforeFailure;
  final String? refreshFailureCode;
  final String protectedFailureCode;
  final bool holdRefresh;
  final Completer<void> _oldRequestsReady = Completer<void>();
  final Completer<void> refreshStarted = Completer<void>();
  final Completer<void> _refreshReleased = Completer<void>();
  int oldAccessRequests = 0;
  int newAccessRequests = 0;
  int refreshRequests = 0;
  final List<String> refreshTokens = [];

  void releaseRefresh() {
    if (!_refreshReleased.isCompleted) {
      _refreshReleased.complete();
    }
  }

  @override
  void close({bool force = false}) {}

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.uri.path == '/api/v1/auth/refresh') {
      refreshRequests++;
      final body = options.data! as Map<String, dynamic>;
      refreshTokens.add(body['refresh_token']! as String);
      if (!refreshStarted.isCompleted) {
        refreshStarted.complete();
      }
      if (holdRefresh) {
        await _refreshReleased.future;
      }
      await Future<void>.delayed(const Duration(milliseconds: 10));
      if (refreshFailureCode case final code?) {
        return _jsonResponse(401, {
          'success': false,
          'error': {'code': code, 'message': 'refresh rejected'},
        });
      }
      return _jsonResponse(200, {
        'success': true,
        'data': {
          'access_token': 'access-new',
          'refresh_token': 'refresh-new',
          'expires_at': '2026-07-19T13:00:00Z',
          'token_type': 'Bearer',
        },
      });
    }

    if (options.uri.path == '/api/v1/protected') {
      final authorization = options.headers['Authorization'];
      if (authorization == 'Bearer access-new') {
        newAccessRequests++;
        return _jsonResponse(200, {
          'success': true,
          'data': {'value': 'ok'},
        });
      }

      oldAccessRequests++;
      if (oldAccessRequests >= oldRequestsBeforeFailure &&
          !_oldRequestsReady.isCompleted) {
        _oldRequestsReady.complete();
      }
      await _oldRequestsReady.future;
      return _jsonResponse(401, {
        'code': protectedFailureCode,
        'message': 'token expired',
      });
    }

    return _jsonResponse(404, {'code': 'NOT_FOUND', 'message': 'not found'});
  }

  ResponseBody _jsonResponse(int statusCode, Object body) {
    return ResponseBody.fromString(
      jsonEncode(body),
      statusCode,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }
}

final class _CommitFailingSessionStore implements AuthSessionStore {
  _CommitFailingSessionStore(AuthSession initialSession)
    : _session = initialSession;

  int _revision = 1;
  AuthSession? _session;

  @override
  Future<AuthSessionSnapshot> snapshot() async {
    return AuthSessionSnapshot(revision: _revision, session: _session);
  }

  @override
  Future<bool> commitIfRevision(
    int expectedRevision,
    AuthSession session,
  ) async {
    if (expectedRevision != _revision) {
      return false;
    }
    throw AuthSessionStoreException(
      'write',
      StateError('simulated secure storage write failure'),
    );
  }

  @override
  Future<AuthSessionSnapshot> takeAndClear() async {
    final previous = _session;
    _revision++;
    _session = null;
    return AuthSessionSnapshot(revision: _revision, session: previous);
  }

  @override
  Future<AuthSessionSnapshot?> takeAndClearIfRevision(
    int expectedRevision,
  ) async {
    if (expectedRevision != _revision) {
      return null;
    }
    return takeAndClear();
  }
}
