import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/files/file_models.dart';
import 'package:mnemonas_client/core/files/files_api.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';

void main() {
  group('UploadSessionSnapshot', () {
    test('strictly parses every protocol state', () {
      for (final state in UploadSessionState.values) {
        final complete =
            state == UploadSessionState.ready ||
            state == UploadSessionState.committing ||
            state == UploadSessionState.committed ||
            state == UploadSessionState.conflict;
        final snapshot = UploadSessionSnapshot.fromJson(
          _snapshotJson(
            state: state,
            durableOffset: complete ? 9 : 3,
            contentBlake3: complete ? _digest('a') : null,
            persistenceWarning: state == UploadSessionState.committed,
          ),
        );

        expect(snapshot.id, 'session-1');
        expect(snapshot.path, '/documents/report.bin');
        expect(snapshot.state, state);
        expect(snapshot.durableOffset, complete ? 9 : 3);
        expect(snapshot.totalBytes, 9);
        expect(snapshot.createdAt, DateTime.utc(2026, 7, 19, 12));
        expect(
          snapshot.updatedAt,
          DateTime.utc(2026, 7, 19, 12, 1, 0, 123, 456),
        );
        expect(snapshot.expiresAt, DateTime.utc(2026, 7, 19, 13));
        expect(snapshot.contentBlake3, complete ? _digest('a') : null);
        expect(
          snapshot.isTerminal,
          state == UploadSessionState.committed ||
              state == UploadSessionState.conflict ||
              state == UploadSessionState.cancelled,
        );
      }
    });

    test('rejects missing, unknown, and mistyped fields', () {
      final valid = _snapshotJson();
      final missing = Map<String, dynamic>.from(valid)..remove('state');

      expect(
        () => UploadSessionSnapshot.fromJson(missing),
        throwsFormatException,
      );
      expect(
        () => UploadSessionSnapshot.fromJson(<String, dynamic>{
          ...valid,
          'access_token': 'must-not-be-accepted',
        }),
        throwsFormatException,
      );
      expect(
        () => UploadSessionSnapshot.fromJson(<String, dynamic>{
          ...valid,
          'durable_offset': 1.0,
        }),
        throwsFormatException,
      );
      expect(
        () => UploadSessionSnapshot.fromJson(<String, dynamic>{
          ...valid,
          'persistence_warning': 0,
        }),
        throwsFormatException,
      );
    });

    test('rejects invalid state, identity, path, range, and timestamps', () {
      final invalid = <Map<String, dynamic>>[
        <String, dynamic>{..._snapshotJson(), 'state': 'expired'},
        <String, dynamic>{..._snapshotJson(), 'id': '../session'},
        <String, dynamic>{
          ..._snapshotJson(),
          'path': '/documents/../report.bin',
        },
        <String, dynamic>{..._snapshotJson(), 'durable_offset': 10},
        <String, dynamic>{
          ..._snapshotJson(),
          'updated_at': '2026-07-19T11:59:59Z',
        },
        <String, dynamic>{
          ..._snapshotJson(),
          'expires_at': '2026-07-19T12:00:00Z',
        },
        <String, dynamic>{..._snapshotJson(), 'created_at': 'not-a-time'},
        <String, dynamic>{
          ..._snapshotJson(),
          'created_at': '2026-02-31T12:00:00Z',
        },
      ];

      for (final json in invalid) {
        expect(
          () => UploadSessionSnapshot.fromJson(json),
          throwsFormatException,
        );
      }
    });

    test('enforces complete payload digest and warning invariants', () {
      expect(
        () => UploadSessionSnapshot.fromJson(
          _snapshotJson(state: UploadSessionState.ready, durableOffset: 9),
        ),
        throwsFormatException,
      );
      expect(
        () => UploadSessionSnapshot.fromJson(
          _snapshotJson(durableOffset: 3, contentBlake3: _digest('a')),
        ),
        throwsFormatException,
      );
      expect(
        () => UploadSessionSnapshot.fromJson(
          _snapshotJson(persistenceWarning: true),
        ),
        throwsFormatException,
      );
      expect(
        () => UploadSessionSnapshot.fromJson(_snapshotJson(durableOffset: 9)),
        throwsFormatException,
      );
      expect(
        () => UploadSessionSnapshot.fromJson(
          _snapshotJson(
            state: UploadSessionState.committed,
            durableOffset: 9,
            contentBlake3: _digest('A'),
          ),
        ),
        throwsFormatException,
      );
    });
  });

  test(
    'create sends the normalized exact request and validates response',
    () async {
      final adapter = _UploadSessionAdapter(totalBytes: 9);
      final api = await _filesApi(adapter);

      final response = await api.createUploadSession(
        logicalPath: 'documents//report.bin',
        totalBytes: 9,
        clientRequestId: 'task-1',
      );

      expect(response.statusCode, HttpStatus.created);
      expect(response.data.state, UploadSessionState.uploading);
      final request = adapter.requests.single;
      expect(request.method, 'POST');
      expect(request.path, '/api/v1/upload-sessions');
      expect(request.data, <String, Object>{
        'path': '/documents/report.bin',
        'total_bytes': 9,
        'client_request_id': 'task-1',
      });
      expect(request.retryOnUnauthorized, isTrue);
    },
  );

  test('create rejects a response for another path or size', () async {
    final adapter = _UploadSessionAdapter(
      totalBytes: 9,
      responsePath: '/other/report.bin',
    );
    final api = await _filesApi(adapter);

    await expectLater(
      api.createUploadSession(
        logicalPath: '/documents/report.bin',
        totalBytes: 9,
        clientRequestId: 'task-1',
      ),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'INVALID_RESPONSE',
        ),
      ),
    );
  });

  test('create accepts an immediately ready zero-byte session', () async {
    final adapter = _UploadSessionAdapter(totalBytes: 0);
    final api = await _filesApi(adapter);

    final response = await api.createUploadSession(
      logicalPath: '/empty.bin',
      totalBytes: 0,
      clientRequestId: 'task-empty',
    );

    expect(response.data.state, UploadSessionState.ready);
    expect(response.data.durableOffset, 0);
    expect(response.data.contentBlake3, _digest('a'));
  });

  test('lookup finds an existing session with a read-only request', () async {
    final adapter = _UploadSessionAdapter(totalBytes: 9);
    final api = await _filesApi(adapter);

    final response = await api.lookupUploadSessionByClientRequestId(
      clientRequestId: 'task-1',
      logicalPath: 'documents//report.bin',
      totalBytes: 9,
    );

    expect(response.data.id, 'session-1');
    expect(response.data.path, '/documents/report.bin');
    expect(response.data.totalBytes, 9);
    final request = adapter.requests.single;
    expect(request.method, 'GET');
    expect(request.path, '/api/v1/upload-sessions/by-client-request/task-1');
    expect(request.data, isNull);
    expect(request.retryOnUnauthorized, isTrue);
  });

  test(
    'lookup strictly rejects mismatched and unknown response data',
    () async {
      for (final adapter in <_UploadSessionAdapter>[
        _UploadSessionAdapter(totalBytes: 9, responsePath: '/other/report.bin'),
        _UploadSessionAdapter(totalBytes: 9, lookupExtraField: true),
      ]) {
        final api = await _filesApi(adapter);

        await expectLater(
          api.lookupUploadSessionByClientRequestId(
            clientRequestId: 'task-1',
            logicalPath: '/documents/report.bin',
            totalBytes: 9,
          ),
          throwsA(
            isA<ApiException>().having(
              (error) => error.code,
              'code',
              'INVALID_RESPONSE',
            ),
          ),
        );
      }
    },
  );

  test('status, commit, and cancel use one snapshot contract', () async {
    final adapter = _UploadSessionAdapter(totalBytes: 9);
    final api = await _filesApi(adapter);

    final status = await api.getUploadSessionStatus(sessionId: 'session-1');
    final committed = await api.commitUploadSession(sessionId: 'session-1');
    final cancelled = await api.cancelUploadSession(sessionId: 'session-1');

    expect(status.data.state, UploadSessionState.uploading);
    expect(committed.data.state, UploadSessionState.committed);
    expect(committed.data.persistenceWarning, isTrue);
    expect(cancelled.data.state, UploadSessionState.cancelled);
    expect(
      adapter.requests.map((request) => (request.method, request.path)),
      <(String, String)>[
        ('GET', '/api/v1/upload-sessions/session-1'),
        ('POST', '/api/v1/upload-sessions/session-1/commit'),
        ('DELETE', '/api/v1/upload-sessions/session-1'),
      ],
    );
    expect(
      adapter.requests.every((request) => request.retryOnUnauthorized),
      isTrue,
    );
  });

  test(
    'status and commit safely refresh and retry a token-expired 401',
    () async {
      for (final operation in <String>['status', 'commit']) {
        final path = operation == 'status'
            ? '/api/v1/upload-sessions/session-1'
            : '/api/v1/upload-sessions/session-1/commit';
        final adapter = _UploadSessionAdapter(
          totalBytes: 9,
          tokenExpiredOncePath: path,
        );
        final api = await _filesApi(adapter);

        if (operation == 'status') {
          await api.getUploadSessionStatus(sessionId: 'session-1');
        } else {
          await api.commitUploadSession(sessionId: 'session-1');
        }

        expect(adapter.refreshRequests, 1, reason: operation);
        expect(adapter.requestCount(path), 2, reason: operation);
        expect(
          adapter.requests
              .where((request) => request.path == path)
              .last
              .authorization,
          'Bearer access-new',
          reason: operation,
        );
      }
    },
  );

  test(
    'chunk reads only the requested bytes and sends exact headers',
    () async {
      final directory = await Directory.systemTemp.createTemp(
        'mnemonas-upload-session-chunk-',
      );
      addTearDown(() => directory.delete(recursive: true));
      final source = File('${directory.path}/payload.bin');
      await source.writeAsBytes(<int>[1, 2, 3, 4, 5, 6, 7, 8, 9], flush: true);
      final adapter = _UploadSessionAdapter(totalBytes: 9, initialOffset: 1);
      final api = await _filesApi(adapter);

      final response = await api.uploadSessionChunk(
        sessionId: 'session-1',
        sourcePath: source.path,
        offset: 1,
        length: 3,
        chunkId: 'task-1:1',
      );

      expect(response.data.durableOffset, 4);
      final request = adapter.requests.single;
      expect(request.method, 'PATCH');
      expect(request.path, '/api/v1/upload-sessions/session-1');
      expect(request.bodyBytes, <int>[2, 3, 4]);
      expect(request.headers[uploadOffsetHeader], '1');
      expect(request.headers[uploadChunkIdHeader], 'task-1:1');
      expect(
        request.headers[uploadChunkSha256Header],
        '1f528ffd2895634c176537c055daa5c0971b7915519999337a0e355410d8fd98',
      );
      expect(request.headers[Headers.contentLengthHeader], '3');
      expect(
        request.headers[Headers.contentTypeHeader],
        'application/octet-stream',
      );
      expect(request.retryOnUnauthorized, isFalse);
    },
  );

  test(
    'chunk proactively refreshes before opening a non-replayable request',
    () async {
      final directory = await Directory.systemTemp.createTemp(
        'mnemonas-upload-session-refresh-',
      );
      addTearDown(() => directory.delete(recursive: true));
      final source = File('${directory.path}/payload.bin');
      await source.writeAsBytes(<int>[1, 2, 3], flush: true);
      final adapter = _UploadSessionAdapter(totalBytes: 3);
      final api = await _filesApi(
        adapter,
        expiresAt: _now.add(const Duration(seconds: 5)),
      );

      await api.uploadSessionChunk(
        sessionId: 'session-1',
        sourcePath: source.path,
        offset: 0,
        length: 3,
        chunkId: 'task-1:0',
      );

      expect(adapter.refreshRequests, 1);
      final patch = adapter.requests.singleWhere(
        (request) => request.method == 'PATCH',
      );
      expect(patch.authorization, 'Bearer access-new');
      expect(patch.retryOnUnauthorized, isFalse);
    },
  );

  test('chunk rejects invalid ranges before making a request', () async {
    final directory = await Directory.systemTemp.createTemp(
      'mnemonas-upload-session-invalid-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final source = File('${directory.path}/payload.bin');
    await source.writeAsBytes(<int>[1, 2, 3], flush: true);
    final adapter = _UploadSessionAdapter(totalBytes: 3);
    final api = await _filesApi(adapter);

    expect(
      () => api.uploadSessionChunk(
        sessionId: 'session-1',
        sourcePath: source.path,
        offset: 0,
        length: maxUploadSessionChunkBytes + 1,
        chunkId: 'task-1:0',
      ),
      throwsArgumentError,
    );
    await expectLater(
      api.uploadSessionChunk(
        sessionId: 'session-1',
        sourcePath: source.path,
        offset: 2,
        length: 2,
        chunkId: 'task-1:2',
      ),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'UPLOAD_CHUNK_SOURCE_INVALID',
        ),
      ),
    );
    expect(adapter.requests, isEmpty);
  });

  test(
    'chunk rejects a response that does not acknowledge all bytes',
    () async {
      final directory = await Directory.systemTemp.createTemp(
        'mnemonas-upload-session-ack-',
      );
      addTearDown(() => directory.delete(recursive: true));
      final source = File('${directory.path}/payload.bin');
      await source.writeAsBytes(<int>[1, 2, 3], flush: true);
      final adapter = _UploadSessionAdapter(
        totalBytes: 3,
        forcedChunkOffset: 2,
      );
      final api = await _filesApi(adapter);

      await expectLater(
        api.uploadSessionChunk(
          sessionId: 'session-1',
          sourcePath: source.path,
          offset: 0,
          length: 3,
          chunkId: 'task-1:0',
        ),
        throwsA(
          isA<ApiException>().having(
            (error) => error.code,
            'code',
            'INVALID_RESPONSE',
          ),
        ),
      );
    },
  );

  test('all methods reject malformed protocol identifiers locally', () async {
    final adapter = _UploadSessionAdapter(totalBytes: 1);
    final api = await _filesApi(adapter);

    expect(
      () => api.createUploadSession(
        logicalPath: '/payload.bin',
        totalBytes: 1,
        clientRequestId: '../task',
      ),
      throwsArgumentError,
    );
    expect(
      () => api.getUploadSessionStatus(sessionId: '../session'),
      throwsArgumentError,
    );
    expect(
      () => api.lookupUploadSessionByClientRequestId(
        clientRequestId: '../task',
        logicalPath: '/payload.bin',
        totalBytes: 1,
      ),
      throwsArgumentError,
    );
    expect(
      () => api.commitUploadSession(sessionId: 'bad/session'),
      throwsArgumentError,
    );
    expect(() => api.cancelUploadSession(sessionId: ''), throwsArgumentError);
    expect(adapter.requests, isEmpty);
  });
}

final _now = DateTime.utc(2026, 7, 19, 12);

Map<String, dynamic> _snapshotJson({
  UploadSessionState state = UploadSessionState.uploading,
  int durableOffset = 3,
  int totalBytes = 9,
  String? contentBlake3,
  bool persistenceWarning = false,
  String id = 'session-1',
  String path = '/documents/report.bin',
}) {
  return <String, dynamic>{
    'id': id,
    'path': path,
    'state': state.name,
    'durable_offset': durableOffset,
    'total_bytes': totalBytes,
    'created_at': '2026-07-19T12:00:00Z',
    'updated_at': '2026-07-19T12:01:00.123456789Z',
    'expires_at': '2026-07-19T13:00:00+00:00',
    'content_blake3': contentBlake3,
    'persistence_warning': persistenceWarning,
  };
}

String _digest(String character) => List<String>.filled(64, character).join();

Future<FilesApi> _filesApi(
  _UploadSessionAdapter adapter, {
  DateTime? expiresAt,
}) async {
  final store = MemoryAuthSessionStore();
  final snapshot = await store.snapshot();
  expect(
    await store.commitIfRevision(
      snapshot.revision,
      AuthSession(
        serverBaseUrl: 'https://nas.example.com',
        tokens: AuthTokenPair(
          accessToken: 'access-old',
          refreshToken: 'refresh-old',
          expiresAt: expiresAt ?? _now.add(const Duration(hours: 2)),
        ),
      ),
    ),
    isTrue,
  );
  final dio = Dio()..httpClientAdapter = adapter;
  return FilesApi(
    ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: dio,
      clock: () => _now,
    ),
  );
}

final class _RequestRecord {
  const _RequestRecord({
    required this.method,
    required this.path,
    required this.data,
    required this.bodyBytes,
    required this.headers,
    required this.retryOnUnauthorized,
    required this.authorization,
  });

  final String method;
  final String path;
  final Object? data;
  final List<int> bodyBytes;
  final Map<String, dynamic> headers;
  final bool retryOnUnauthorized;
  final String? authorization;
}

final class _UploadSessionAdapter implements HttpClientAdapter {
  _UploadSessionAdapter({
    required this.totalBytes,
    this.initialOffset = 0,
    this.forcedChunkOffset,
    this.responsePath,
    this.lookupExtraField = false,
    this.tokenExpiredOncePath,
  });

  final int totalBytes;
  final int initialOffset;
  final int? forcedChunkOffset;
  final String? responsePath;
  final bool lookupExtraField;
  final String? tokenExpiredOncePath;
  final List<_RequestRecord> requests = <_RequestRecord>[];
  final Set<String> _expiredPaths = <String>{};
  int refreshRequests = 0;

  int requestCount(String path) =>
      requests.where((request) => request.path == path).length;

  @override
  void close({bool force = false}) {}

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final bodyBytes = <int>[];
    if (requestStream != null) {
      await for (final chunk in requestStream) {
        bodyBytes.addAll(chunk);
      }
    }
    requests.add(
      _RequestRecord(
        method: options.method,
        path: options.uri.path,
        data: options.data,
        bodyBytes: List<int>.unmodifiable(bodyBytes),
        headers: Map<String, dynamic>.from(options.headers),
        retryOnUnauthorized:
            options.extra['mnemonas.retryUnauthorized'] != false,
        authorization: options.headers['Authorization'] as String?,
      ),
    );

    if (options.uri.path == '/api/v1/auth/refresh') {
      refreshRequests++;
      return _json(HttpStatus.ok, <String, Object>{
        'success': true,
        'data': <String, Object>{
          'access_token': 'access-new',
          'refresh_token': 'refresh-new',
          'expires_at': '2026-07-19T14:00:00Z',
          'token_type': 'Bearer',
        },
      });
    }

    if (options.uri.path == tokenExpiredOncePath &&
        options.headers['Authorization'] == 'Bearer access-old' &&
        _expiredPaths.add(options.uri.path)) {
      return _json(HttpStatus.unauthorized, <String, Object>{
        'code': 'TOKEN_EXPIRED',
        'message': 'token expired',
      });
    }

    if (options.uri.path == '/api/v1/upload-sessions' &&
        options.method == 'POST') {
      final body = options.data! as Map<String, dynamic>;
      final requestedTotal = body['total_bytes']! as int;
      final complete = requestedTotal == 0;
      return _json(HttpStatus.created, <String, Object>{
        'success': true,
        'data': _snapshotJson(
          state: complete
              ? UploadSessionState.ready
              : UploadSessionState.uploading,
          durableOffset: initialOffset,
          totalBytes: requestedTotal,
          contentBlake3: complete ? _digest('a') : null,
          path: responsePath ?? body['path']! as String,
        ),
      });
    }

    if (options.uri.path ==
            '/api/v1/upload-sessions/by-client-request/task-1' &&
        options.method == 'GET') {
      final snapshot = _snapshotJson(
        durableOffset: initialOffset,
        totalBytes: totalBytes,
        path: responsePath ?? '/documents/report.bin',
      );
      if (lookupExtraField) {
        snapshot['client_request_id'] = 'task-1';
      }
      return _json(HttpStatus.ok, <String, Object>{
        'success': true,
        'data': snapshot,
      });
    }

    if (options.uri.path == '/api/v1/upload-sessions/session-1') {
      if (options.method == 'PATCH') {
        final offset = int.parse('${options.headers[uploadOffsetHeader]}');
        final acknowledged = forcedChunkOffset ?? offset + bodyBytes.length;
        final complete = acknowledged == totalBytes;
        return _json(HttpStatus.ok, <String, Object>{
          'success': true,
          'data': _snapshotJson(
            state: complete
                ? UploadSessionState.ready
                : UploadSessionState.uploading,
            durableOffset: acknowledged,
            totalBytes: totalBytes,
            contentBlake3: complete ? _digest('a') : null,
          ),
        });
      }
      if (options.method == 'DELETE') {
        return _json(HttpStatus.ok, <String, Object>{
          'success': true,
          'data': _snapshotJson(
            state: UploadSessionState.cancelled,
            durableOffset: initialOffset,
            totalBytes: totalBytes,
          ),
        });
      }
      return _json(HttpStatus.ok, <String, Object>{
        'success': true,
        'data': _snapshotJson(
          durableOffset: initialOffset,
          totalBytes: totalBytes,
        ),
      });
    }

    if (options.uri.path == '/api/v1/upload-sessions/session-1/commit') {
      return _json(HttpStatus.ok, <String, Object>{
        'success': true,
        'data': _snapshotJson(
          state: UploadSessionState.committed,
          durableOffset: totalBytes,
          totalBytes: totalBytes,
          contentBlake3: _digest('a'),
          persistenceWarning: true,
        ),
      });
    }

    return _json(HttpStatus.notFound, <String, Object>{
      'code': 'NOT_FOUND',
      'message': 'not found',
    });
  }

  ResponseBody _json(int statusCode, Object body) {
    return ResponseBody.fromString(
      jsonEncode(body),
      statusCode,
      headers: <String, List<String>>{
        Headers.contentTypeHeader: <String>[Headers.jsonContentType],
      },
    );
  }
}
