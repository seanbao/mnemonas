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
  test(
    'logout revokes by refresh-token body without requiring bearer',
    () async {
      final store = MemoryAuthSessionStore();
      await store.save(_session());
      final adapter = _LogoutAdapter();
      final dio = Dio()..httpClientAdapter = adapter;
      final api = AuthApi(
        ApiClient(
          endpoint: ServerEndpoint.parse('https://nas.example.com'),
          sessionStore: store,
          dio: dio,
        ),
      );

      final response = await api.logout();

      expect(response.statusCode, 200);
      expect(adapter.authorization, isNull);
      expect(adapter.refreshToken, 'refresh-token');
      expect(await store.load(), isNull);
    },
  );

  test('logout keeps local session when server revocation fails', () async {
    final store = MemoryAuthSessionStore();
    await store.save(_session());
    final adapter = _LogoutAdapter(fail: true);
    final dio = Dio()..httpClientAdapter = adapter;
    final api = AuthApi(
      ApiClient(
        endpoint: ServerEndpoint.parse('https://nas.example.com'),
        sessionStore: store,
        dio: dio,
      ),
    );

    await expectLater(api.logout(), throwsA(isA<ApiException>()));
    expect(await store.load(), isNotNull);
  });
}

AuthSession _session() {
  return AuthSession(
    serverBaseUrl: 'https://nas.example.com',
    tokens: AuthTokenPair(
      accessToken: 'access-token',
      refreshToken: 'refresh-token',
      expiresAt: DateTime.utc(2026, 7, 19, 13),
    ),
  );
}

final class _LogoutAdapter implements HttpClientAdapter {
  _LogoutAdapter({this.fail = false});

  final bool fail;
  String? authorization;
  String? refreshToken;

  @override
  void close({bool force = false}) {}

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    authorization = options.headers['Authorization'] as String?;
    refreshToken =
        (options.data! as Map<String, dynamic>)['refresh_token'] as String?;
    return ResponseBody.fromString(
      jsonEncode(
        fail
            ? {'code': 'TOKEN_ERROR', 'message': 'revocation failed'}
            : {'success': true, 'data': null, 'message': 'logged out'},
      ),
      fail ? 500 : 200,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }
}
