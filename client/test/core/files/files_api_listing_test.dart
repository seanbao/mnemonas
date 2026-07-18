import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/files/files_api.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';

void main() {
  test(
    'list normalizes the request and accepts the matching response path',
    () async {
      final harness = await _Harness.create(responsePath: '/文档');

      final response = await harness.api.list('文档//');

      expect(response.data.path, '/文档');
      expect(harness.adapter.requestPath, '/api/v1/files/%E6%96%87%E6%A1%A3');
    },
  );

  test('list rejects a canonical response path for another request', () async {
    final harness = await _Harness.create(responsePath: '/其他');

    await expectLater(
      harness.api.list('/文档'),
      throwsA(
        isA<ApiException>().having(
          (ApiException error) => error.code,
          'code',
          'INVALID_RESPONSE',
        ),
      ),
    );
  });
}

final class _Harness {
  const _Harness({required this.api, required this.adapter});

  static Future<_Harness> create({required String responsePath}) async {
    final store = MemoryAuthSessionStore();
    final snapshot = await store.snapshot();
    expect(
      await store.commitIfRevision(
        snapshot.revision,
        AuthSession(
          serverBaseUrl: 'https://nas.example.com',
          tokens: AuthTokenPair(
            accessToken: 'access-token',
            refreshToken: 'refresh-token',
            expiresAt: DateTime.utc(2099),
          ),
        ),
      ),
      isTrue,
    );
    final adapter = _ListingAdapter(responsePath);
    final dio = Dio()..httpClientAdapter = adapter;
    final client = ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: dio,
    );
    return _Harness(api: FilesApi(client), adapter: adapter);
  }

  final FilesApi api;
  final _ListingAdapter adapter;
}

final class _ListingAdapter implements HttpClientAdapter {
  _ListingAdapter(this.responsePath);

  final String responsePath;
  String? requestPath;

  @override
  void close({bool force = false}) {}

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requestPath = options.path;
    return ResponseBody.fromString(
      jsonEncode(<String, Object?>{
        'success': true,
        'data': <String, Object?>{
          'path': responsePath,
          'capabilities': <String, Object?>{
            'read': true,
            'concreteRead': true,
            'write': true,
          },
          'deleteMode': 'trash',
          'deletePolicyToken': List<String>.filled(64, 'a').join(),
          'trashRetentionDays': 30,
          'trashAutoCleanupEnabled': true,
          'files': <Object?>[],
        },
      }),
      200,
      headers: <String, List<String>>{
        Headers.contentTypeHeader: <String>[Headers.jsonContentType],
      },
    );
  }
}
