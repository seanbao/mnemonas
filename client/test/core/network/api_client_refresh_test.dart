import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';

void main() {
  final now = DateTime.utc(2026, 7, 19, 12);

  test('concurrent 401 responses share one rotating refresh request', () async {
    final store = MemoryAuthSessionStore();
    await store.save(
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
    final saved = await store.load();
    expect(saved?.tokens.accessToken, 'access-new');
    expect(saved?.tokens.refreshToken, 'refresh-new');
    expect(saved?.lastRefreshAt, now);
  });

  test('local cooldown prevents a second rotation within 30 seconds', () async {
    final store = MemoryAuthSessionStore();
    await store.save(
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
    await store.save(
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
    expect(await store.load(), isNull);
  });

  test('does not refresh non-expiry 401 responses', () async {
    final store = MemoryAuthSessionStore();
    await store.save(_session(expiresAt: now.add(const Duration(minutes: 1))));
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
}

AuthSession _session({required DateTime expiresAt, DateTime? lastRefreshAt}) {
  return AuthSession(
    serverBaseUrl: 'https://nas.example.com',
    tokens: AuthTokenPair(
      accessToken: 'access-old',
      refreshToken: 'refresh-old',
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
  });

  final int oldRequestsBeforeFailure;
  final String? refreshFailureCode;
  final String protectedFailureCode;
  final Completer<void> _oldRequestsReady = Completer<void>();
  int oldAccessRequests = 0;
  int newAccessRequests = 0;
  int refreshRequests = 0;
  final List<String> refreshTokens = [];

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
