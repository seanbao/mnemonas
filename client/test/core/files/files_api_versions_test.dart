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
  test(
    'lists a strictly ordered history bound to the requested path',
    () async {
      final adapter = _VersionAdapter();
      final api = await _filesApi(adapter);

      final response = await api.listVersions('/文档/report a.txt');

      expect(response.statusCode, HttpStatus.ok);
      expect(response.data.path, '/文档/report a.txt');
      expect(response.data.versions, hasLength(2));
      expect(response.data.current.sequence, 1);
      expect(response.data.current.isCurrent, isTrue);
      expect(response.data.current.hash, _hash('a'));
      expect(response.data.current.timestamp, DateTime.utc(2026, 7, 19, 4));
      expect(response.data.versions[1].sequence, 2);
      expect(response.data.versions[1].isCurrent, isFalse);

      final request = adapter.requests.single;
      expect(request.method, 'GET');
      expect(
        request.path,
        '/api/v1/versions/'
        '%E6%96%87%E6%A1%A3/report%20a.txt',
      );
      expect(request.query, isEmpty);
    },
  );

  test('rejects malformed history fields and ordering', () {
    final valid = _historyData();
    final invalidPayloads = <Map<String, dynamic>>[
      {...valid, 'path': '/other.txt'},
      {
        ...valid,
        'versions': [
          {...(valid['versions']! as List).first as Map, 'hash': _hash('A')},
          (valid['versions']! as List)[1],
        ],
      },
      {
        ...valid,
        'versions': [
          {...(valid['versions']! as List).first as Map, 'size': -1},
          (valid['versions']! as List)[1],
        ],
      },
      {
        ...valid,
        'versions': [
          {
            ...(valid['versions']! as List).first as Map,
            'timestamp': '2026-07-19',
          },
          (valid['versions']! as List)[1],
        ],
      },
      {
        ...valid,
        'versions': [
          (valid['versions']! as List).first,
          {...(valid['versions']! as List)[1] as Map, 'version': 3},
        ],
      },
      {...valid, 'versions': <Object>[]},
      {
        ...valid,
        'versions': [
          {
            ...(valid['versions']! as List).first as Map,
            'comment': 'hidden\u0000comment',
          },
          (valid['versions']! as List)[1],
        ],
      },
      {
        ...valid,
        'versions': [
          {
            ...(valid['versions']! as List).first as Map,
            'comment': List.filled(257, 'a').join(),
          },
          (valid['versions']! as List)[1],
        ],
      },
      for (final bidiControl in <String>[
        '\u202a',
        '\u202e',
        '\u2066',
        '\u2069',
      ])
        {
          ...valid,
          'versions': [
            {
              ...(valid['versions']! as List).first as Map,
              'comment': 'misleading${bidiControl}comment',
            },
            (valid['versions']! as List)[1],
          ],
        },
    ];

    for (final payload in invalidPayloads) {
      expect(
        () => FileVersionHistory.fromJson(
          payload,
          expectedPath: '/文档/report a.txt',
        ),
        throwsFormatException,
      );
    }
  });

  test('accepts bounded comments without using them as current identity', () {
    final payload = _historyData();
    final encoded = payload['versions']! as List<dynamic>;
    payload['versions'] = <dynamic>[
      {
        ...Map<String, dynamic>.from(encoded[0] as Map),
        'comment': List.filled(256, '界').join(),
      },
      ...encoded.skip(1),
    ];

    final history = FileVersionHistory.fromJson(
      payload,
      expectedPath: '/文档/report a.txt',
    );

    expect(history.current.isCurrent, isTrue);
    expect(history.current.comment, hasLength(256));
  });

  test('requires lowercase BLAKE3 file-entry content identity', () {
    final valid = _fileEntryJson(hash: _hash('a'));
    final entry = FileEntry.fromJson(valid);
    final missing = FileEntry.fromJson(_fileEntryJson());

    expect(entry.hasVerifiableContentIdentity, isTrue);
    expect(missing.hasVerifiableContentIdentity, isFalse);
    expect(
      () => FileEntry.fromJson(_fileEntryJson(hash: _hash('A'))),
      throwsFormatException,
    );
  });

  test(
    'refreshes before restore and disables automatic unauthorized replay',
    () async {
      final adapter = _VersionAdapter(restoreWarning: true);
      final api = await _filesApi(
        adapter,
        expiresAt: _now.add(const Duration(seconds: 5)),
      );

      final response = await api.restoreVersion(
        logicalPath: '文档//report a.txt',
        hash: _hash('b'),
      );

      expect(response.data.path, '/文档/report a.txt');
      expect(response.data.restoredHash, _hash('b'));
      expect(response.data.persistenceWarning, isTrue);
      expect(
        response.warnings.map((warning) => warning.value),
        contains(versionRestorePersistenceWarningHeader),
      );
      expect(adapter.refreshRequests, 1);
      expect(adapter.requests.map((request) => request.path), <String>[
        '/api/v1/auth/refresh',
        '/api/v1/versions/${_hash('b')}/restore',
      ]);
      final request = adapter.requests.last;
      expect(request.method, 'POST');
      expect(request.path, '/api/v1/versions/${_hash('b')}/restore');
      expect(request.query, {'path': '/文档/report a.txt'});
      expect(request.data, isNull);
      expect(request.authorization, 'Bearer access-new');
      expect(request.retryOnUnauthorized, isFalse);
    },
  );

  test('rejects a restore warning that is inconsistent with headers', () async {
    final adapter = _VersionAdapter(
      restoreWarning: true,
      omitRestoreWarningHeader: true,
    );
    final api = await _filesApi(adapter);

    await expectLater(
      api.restoreVersion(logicalPath: '/文档/report a.txt', hash: _hash('b')),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'INVALID_RESPONSE',
        ),
      ),
    );
  });

  test('downloads a historical version with an exact strong ETag', () async {
    final directory = await Directory.systemTemp.createTemp(
      'mnemonas-version-download-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final bytes = <int>[1, 2, 3, 4];
    final adapter = _VersionAdapter(downloadBytes: bytes);
    final api = await _filesApi(adapter);
    final version = _version(size: bytes.length);
    final progress = <(int, int)>[];

    final result = await api.downloadVersion(
      logicalPath: '/文档/report a.txt',
      version: version,
      destinationPath: '${directory.path}/report a.txt',
      onProgress: (transferred, total) {
        progress.add((transferred, total));
      },
    );

    expect(await result.file.readAsBytes(), bytes);
    expect(result.contentHash, version.hash);
    expect(result.totalBytes, bytes.length);
    expect(result.bytesWritten, bytes.length);
    expect(progress.last, (bytes.length, bytes.length));
    final request = adapter.requests.single;
    expect(request.method, 'GET');
    expect(request.path, '/api/v1/download/%E6%96%87%E6%A1%A3/report%20a.txt');
    expect(request.query, {'version': version.hash});
    expect(request.headers[downloadIdentityConditionHeader], isNull);
    expect(request.headers['Range'], isNull);
  });

  test(
    'rejects historical downloads with a mismatched ETag or length',
    () async {
      final directory = await Directory.systemTemp.createTemp(
        'mnemonas-version-invalid-',
      );
      addTearDown(() => directory.delete(recursive: true));

      for (final adapter in <_VersionAdapter>[
        _VersionAdapter(downloadETag: '"${_hash('c')}"'),
        _VersionAdapter(downloadLength: 5),
        _VersionAdapter(downloadBytes: const <int>[1, 2]),
      ]) {
        final api = await _filesApi(adapter);
        final destination = File(
          '${directory.path}/result-${adapter.hashCode}.bin',
        );
        await expectLater(
          api.downloadVersion(
            logicalPath: '/文档/report a.txt',
            version: _version(),
            destinationPath: destination.path,
          ),
          throwsA(
            isA<ApiException>().having(
              (error) => error.kind,
              'kind',
              ApiFailureKind.invalidResponse,
            ),
          ),
        );
        expect(await destination.exists(), isFalse);
      }
    },
  );

  test('rejects a version from another path without a request', () async {
    final adapter = _VersionAdapter();
    final api = await _filesApi(adapter);
    final version = FileVersion(
      path: '/other.txt',
      sequence: 2,
      hash: _hash('b'),
      size: 4,
      timestamp: DateTime.utc(2026, 7, 18),
    );

    await expectLater(
      api.downloadVersion(
        logicalPath: '/文档/report a.txt',
        version: version,
        destinationPath: '/tmp/unreachable-version-result',
      ),
      throwsFormatException,
    );
    expect(adapter.requests, isEmpty);
  });

  test('rejects the current version without a request', () async {
    final adapter = _VersionAdapter();
    final api = await _filesApi(adapter);
    final version = FileVersion(
      path: '/文档/report a.txt',
      sequence: 1,
      hash: _hash('a'),
      size: 4,
      timestamp: DateTime.utc(2026, 7, 19),
      comment: '(current)',
    );

    await expectLater(
      api.downloadVersion(
        logicalPath: '/文档/report a.txt',
        version: version,
        destinationPath: '/tmp/unreachable-current-version-result',
      ),
      throwsFormatException,
    );
    expect(adapter.requests, isEmpty);
  });
}

FileVersion _version({int size = 4}) {
  return FileVersion(
    path: '/文档/report a.txt',
    sequence: 2,
    hash: _hash('b'),
    size: size,
    timestamp: DateTime.utc(2026, 7, 18, 12),
    comment: 'before restore',
  );
}

Map<String, dynamic> _historyData() => {
  'path': '/文档/report a.txt',
  'versions': [
    {
      'version': 1,
      'hash': _hash('a'),
      'size': 4,
      'timestamp': '2026-07-19T12:00:00+08:00',
      'comment': '(current)',
    },
    {
      'version': 2,
      'hash': _hash('b'),
      'size': 4,
      'timestamp': '2026-07-18T12:00:00Z',
      'comment': 'before restore',
    },
  ],
};

Map<String, dynamic> _fileEntryJson({String? hash}) => {
  'name': 'report a.txt',
  'path': '/文档/report a.txt',
  'isDir': false,
  'size': 4,
  'modTime': '2026-07-19T12:00:00+08:00',
  'hash': hash,
  'versioned': true,
  'capabilities': {'read': true, 'concreteRead': true, 'write': true},
};

String _hash(String character) => List.filled(64, character).join();

final _now = DateTime.utc(2026, 7, 19, 12);

Future<FilesApi> _filesApi(
  HttpClientAdapter adapter, {
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
          expiresAt: expiresAt ?? _now.add(const Duration(hours: 1)),
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
    required this.query,
    required this.headers,
    required this.data,
    required this.retryOnUnauthorized,
    required this.authorization,
  });

  final String method;
  final String path;
  final Map<String, dynamic> query;
  final Map<String, dynamic> headers;
  final Object? data;
  final bool retryOnUnauthorized;
  final String? authorization;
}

final class _VersionAdapter implements HttpClientAdapter {
  _VersionAdapter({
    this.restoreWarning = false,
    this.omitRestoreWarningHeader = false,
    this.downloadBytes = const <int>[1, 2, 3, 4],
    String? downloadETag,
    this.downloadLength,
  }) : downloadETag = downloadETag ?? '"${_hash('b')}"';

  final bool restoreWarning;
  final bool omitRestoreWarningHeader;
  final List<int> downloadBytes;
  final String downloadETag;
  final int? downloadLength;
  final List<_RequestRecord> requests = [];
  int refreshRequests = 0;

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
        query: Map<String, dynamic>.from(options.queryParameters),
        headers: Map<String, dynamic>.from(options.headers),
        data: options.data,
        retryOnUnauthorized:
            options.extra['mnemonas.retryUnauthorized'] != false,
        authorization: options.headers['Authorization'] as String?,
      ),
    );

    if (options.uri.path == '/api/v1/auth/refresh') {
      refreshRequests++;
      return _json(HttpStatus.ok, {
        'success': true,
        'data': {
          'access_token': 'access-new',
          'refresh_token': 'refresh-new',
          'expires_at': '2026-07-19T14:00:00Z',
          'token_type': 'Bearer',
        },
      });
    }
    if (options.uri.path.startsWith('/api/v1/versions/') &&
        options.uri.path.endsWith('/restore')) {
      return _json(
        HttpStatus.ok,
        {
          'success': true,
          'data': {
            'path': '/文档/report a.txt',
            'restored': _hash('b'),
            if (restoreWarning) 'warning': true,
          },
          'message': restoreWarning
              ? 'version restored with persistence warning'
              : 'version restored successfully',
        },
        warning: restoreWarning && !omitRestoreWarningHeader
            ? versionRestorePersistenceWarningHeader
            : null,
      );
    }
    if (options.uri.path.startsWith('/api/v1/versions/')) {
      return _json(HttpStatus.ok, {'success': true, 'data': _historyData()});
    }
    if (options.uri.path.startsWith('/api/v1/download/')) {
      return ResponseBody.fromBytes(
        downloadBytes,
        HttpStatus.ok,
        headers: {
          HttpHeaders.etagHeader: [downloadETag],
          Headers.contentLengthHeader: [
            '${downloadLength ?? downloadBytes.length}',
          ],
        },
      );
    }
    return _json(HttpStatus.notFound, {
      'code': 'NOT_FOUND',
      'message': 'not found',
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
