import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';
import 'package:mnemonas_client/core/trash/trash_api.dart';
import 'package:mnemonas_client/core/trash/trash_models.dart';

void main() {
  test('lists trash with policy through the authenticated route', () async {
    final harness = await _Harness.create();
    addTearDown(harness.close);

    final response = await harness.api.list();

    expect(response.statusCode, 200);
    expect(response.data.count, 2);
    expect(response.data.totalSize, 42);
    expect(response.data.policy.retentionDays, 30);
    expect(harness.adapter.requests.single.method, 'GET');
    expect(harness.adapter.requests.single.path, '/api/v1/trash/');
    expect(
      harness.adapter.requests.single.authorization,
      'Bearer access-token',
    );
  });

  test('restores to the original or a normalized custom path', () async {
    final harness = await _Harness.create();
    addTearDown(harness.close);

    final original = await harness.api.restore(id: 'item-a');
    final custom = await harness.api.restore(
      id: 'item_B',
      destinationPath: 'archive//恢复 文件.txt',
    );

    expect(original.data.id, 'item-a');
    expect(original.data.persistenceWarning, isFalse);
    expect(custom.data.id, 'item_B');
    expect(custom.data.persistenceWarning, isTrue);
    expect(custom.warnings, hasLength(1));

    expect(harness.adapter.requests[0].method, 'POST');
    expect(harness.adapter.requests[0].path, '/api/v1/trash/item-a/restore');
    expect(harness.adapter.requests[0].query, isEmpty);
    expect(harness.adapter.requests[1].path, '/api/v1/trash/item_B/restore');
    expect(harness.adapter.requests[1].query, {'path': '/archive/恢复 文件.txt'});
    expect(
      harness.adapter.requests[1].uri,
      contains('path=%2Farchive%2F%E6%81%A2%E5%A4%8D+%E6%96%87%E4%BB%B6.txt'),
    );
  });

  test('permanently deletes one exact trash item', () async {
    final harness = await _Harness.create();
    addTearDown(harness.close);

    final result = await harness.api.deletePermanently('item-a');

    expect(result.data.id, 'item-a');
    expect(result.data.cleanupWarning, isTrue);
    expect(result.warnings, hasLength(1));
    expect(harness.adapter.requests.single.method, 'DELETE');
    expect(harness.adapter.requests.single.path, '/api/v1/trash/item-a');
  });

  test(
    'submits the frozen exact ID selection and validates its partition',
    () async {
      final harness = await _Harness.create();
      addTearDown(harness.close);
      final source = <String>['item-a', 'item-b', 'item-c'];
      final selection = TrashSelectionSnapshot.fromIds(source);
      source
        ..clear()
        ..add('different');

      final result = await harness.api.emptySelection(selection);

      expect(result.data.deleted, ['item-a', 'item-c']);
      expect(result.data.remaining, ['item-b']);
      expect(result.data.skipped, isEmpty);
      expect(result.data.partial, isTrue);
      expect(harness.adapter.requests.single.method, 'POST');
      expect(harness.adapter.requests.single.path, '/api/v1/trash/empty');
      expect(harness.adapter.requests.single.data, {
        'ids': ['item-a', 'item-b', 'item-c'],
      });
    },
  );

  test(
    'rejects invalid IDs and custom paths before sending a request',
    () async {
      final harness = await _Harness.create();
      addTearDown(harness.close);

      expect(() => harness.api.restore(id: 'item/a'), throwsFormatException);
      expect(
        () => harness.api.restore(id: 'item-a', destinationPath: '/'),
        throwsFormatException,
      );
      expect(() => harness.api.deletePermanently(''), throwsFormatException);
      expect(harness.adapter.requests, isEmpty);
    },
  );

  test('rejects mismatched mutation IDs as invalid server responses', () async {
    final harness = await _Harness.create(mismatchedMutationId: true);
    addTearDown(harness.close);

    await expectLater(
      harness.api.restore(id: 'item-a'),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'INVALID_RESPONSE',
        ),
      ),
    );
  });

  test('rejects incomplete empty-selection partitions', () async {
    final harness = await _Harness.create(invalidPartition: true);
    addTearDown(harness.close);
    final selection = TrashSelectionSnapshot.fromIds(const [
      'item-a',
      'item-b',
      'item-c',
    ]);

    await expectLater(
      harness.api.emptySelection(selection),
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

  static Future<_Harness> create({
    bool mismatchedMutationId = false,
    bool invalidPartition = false,
  }) async {
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

    final adapter = _TrashAdapter(
      mismatchedMutationId: mismatchedMutationId,
      invalidPartition: invalidPartition,
    );
    final dio = Dio()..httpClientAdapter = adapter;
    final client = ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: dio,
    );
    return _Harness(api: TrashApi(client), client: client, adapter: adapter);
  }

  final TrashApi api;
  final ApiClient client;
  final _TrashAdapter adapter;

  void close() => client.close();
}

final class _RequestRecord {
  const _RequestRecord({
    required this.method,
    required this.path,
    required this.uri,
    required this.query,
    required this.data,
    required this.authorization,
  });

  final String method;
  final String path;
  final String uri;
  final Map<String, dynamic> query;
  final Object? data;
  final String? authorization;
}

final class _TrashAdapter implements HttpClientAdapter {
  _TrashAdapter({
    required this.mismatchedMutationId,
    required this.invalidPartition,
  });

  final bool mismatchedMutationId;
  final bool invalidPartition;
  final List<_RequestRecord> requests = [];

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
        method: options.method,
        path: options.uri.path,
        uri: options.uri.toString(),
        query: Map<String, dynamic>.from(options.queryParameters),
        data: options.data,
        authorization: options.headers['Authorization'] as String?,
      ),
    );

    if (options.uri.path == '/api/v1/trash/' && options.method == 'GET') {
      return _json(200, {'success': true, 'data': _listingJson()});
    }
    if (options.uri.path == '/api/v1/trash/empty' && options.method == 'POST') {
      final ids = List<String>.from(
        (options.data! as Map<String, dynamic>)['ids']! as List<dynamic>,
      );
      return _json(200, {
        'success': true,
        'data': {
          'deleted': [ids.first, ids.last],
          'remaining': invalidPartition ? <String>[] : [ids[1]],
          'skipped': <String>[],
          'deleted_count': 2,
          'remaining_count': invalidPartition ? 0 : 1,
          'skipped_count': 0,
          'partial': !invalidPartition,
          'warning': false,
        },
      });
    }
    if (options.uri.path.endsWith('/restore') && options.method == 'POST') {
      final requestedId =
          options.uri.pathSegments[options.uri.pathSegments.length - 2];
      return _json(
        200,
        {
          'success': true,
          'data': {
            'id': mismatchedMutationId ? 'different' : requestedId,
            'restored': true,
            if (requestedId == 'item_B') 'warning': true,
          },
        },
        warning: requestedId == 'item_B'
            ? '199 MnemoNAS "workspace mutation persistence incomplete"'
            : null,
      );
    }
    if (options.uri.path.startsWith('/api/v1/trash/') &&
        options.method == 'DELETE') {
      final requestedId = options.uri.pathSegments.last;
      return _json(200, {
        'success': true,
        'data': {
          'id': mismatchedMutationId ? 'different' : requestedId,
          'deleted': true,
          'warning': true,
        },
      }, warning: '199 MnemoNAS "trash cleanup incomplete"');
    }
    return _json(404, {
      'error': {'code': 'NOT_FOUND', 'message': 'not found'},
    });
  }

  ResponseBody _json(int statusCode, Object body, {String? warning}) {
    return ResponseBody.fromString(
      jsonEncode(body),
      statusCode,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
        if (warning != null) 'warning': [warning],
      },
    );
  }
}

Map<String, dynamic> _listingJson() => {
  'items': [
    {
      'id': 'item-a',
      'originalPath': '/docs/report.txt',
      'deletedAt': '2026-07-19T12:00:00+08:00',
      'expiresAt': '2026-08-18T12:00:00+08:00',
      'name': 'report.txt',
      'isDir': false,
      'size': 42,
      'hadVersions': true,
    },
    {
      'id': 'item-b',
      'originalPath': '/photos/家庭相册',
      'deletedAt': '2026-07-19T12:30:00+08:00',
      'expiresAt': '2026-08-18T12:30:00+08:00',
      'name': '家庭相册',
      'isDir': true,
      'size': 0,
      'hadVersions': false,
    },
  ],
  'count': 2,
  'totalSize': 42,
  'retentionDays': 30,
  'retentionEnabled': true,
  'retentionMaxSize': 1024,
  'trashAutoCleanupEnabled': true,
};
