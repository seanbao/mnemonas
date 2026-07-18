import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/search/search_api.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';

void main() {
  test('submits a trimmed authenticated query with the client limit', () async {
    final harness = await _Harness.create();
    addTearDown(harness.close);

    final response = await harness.api.search('  report  ');

    expect(response.statusCode, 200);
    expect(response.data.query, 'report');
    expect(response.data.count, 1);
    expect(harness.adapter.requests.single.path, '/api/v1/search');
    expect(harness.adapter.requests.single.query, {
      'q': 'report',
      'limit': 100,
    });
    expect(
      harness.adapter.requests.single.authorization,
      'Bearer access-token',
    );
  });

  test('sends and enforces a non-default result limit', () async {
    final harness = await _Harness.create();
    addTearDown(harness.close);

    final response = await harness.api.search('照片', limit: 25);

    expect(response.data.limit, 25);
    expect(harness.adapter.requests.single.query, {'q': '照片', 'limit': 25});
  });

  test('rejects invalid requests before network access', () async {
    final harness = await _Harness.create();
    addTearDown(harness.close);

    expect(() => harness.api.search('   '), throwsFormatException);
    expect(() => harness.api.search('report', limit: 0), throwsFormatException);
    expect(harness.adapter.requests, isEmpty);
  });

  test('rejects a response for a different query', () async {
    final harness = await _Harness.create(mismatchedQuery: true);
    addTearDown(harness.close);

    await expectLater(
      harness.api.search('report'),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'INVALID_RESPONSE',
        ),
      ),
    );
  });
}

final class _Harness {
  const _Harness({
    required this.api,
    required this.client,
    required this.adapter,
  });

  static Future<_Harness> create({bool mismatchedQuery = false}) async {
    final store = MemoryAuthSessionStore();
    final snapshot = await store.snapshot();
    final committed = await store.commitIfRevision(
      snapshot.revision,
      AuthSession(
        serverBaseUrl: 'https://nas.example.com',
        tokens: AuthTokenPair(
          accessToken: 'access-token',
          refreshToken: 'refresh-token',
          expiresAt: DateTime.utc(2099),
        ),
      ),
    );
    if (!committed) {
      throw StateError('Unable to initialize the test session');
    }

    final adapter = _SearchAdapter(mismatchedQuery: mismatchedQuery);
    final client = ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: Dio()..httpClientAdapter = adapter,
    );
    return _Harness(api: SearchApi(client), client: client, adapter: adapter);
  }

  final SearchApi api;
  final ApiClient client;
  final _SearchAdapter adapter;

  void close() => client.close();
}

final class _RequestRecord {
  const _RequestRecord({
    required this.path,
    required this.query,
    required this.authorization,
  });

  final String path;
  final Map<String, dynamic> query;
  final String? authorization;
}

final class _SearchAdapter implements HttpClientAdapter {
  _SearchAdapter({required this.mismatchedQuery});

  final bool mismatchedQuery;
  final List<_RequestRecord> requests = <_RequestRecord>[];

  @override
  void close({bool force = false}) {}

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requests.add(
      _RequestRecord(
        path: options.uri.path,
        query: Map<String, dynamic>.from(options.queryParameters),
        authorization: options.headers['Authorization'] as String?,
      ),
    );
    final query = options.queryParameters['q']! as String;
    return ResponseBody.fromString(
      jsonEncode({
        'success': true,
        'data': {
          'query': mismatchedQuery ? 'different' : query,
          'results': [
            {
              'name': '$query.txt',
              'path': '/docs/$query.txt',
              'isDir': false,
              'size': 42,
              'modTime': '2026-07-19T12:00:00Z',
            },
          ],
          'count': 1,
        },
      }),
      200,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }
}
