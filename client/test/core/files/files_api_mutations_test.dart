import 'dart:convert';
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
    'move, rename, and copy match server request and response contracts',
    () async {
      final harness = await _Harness.create();

      final moved = await harness.api.move(
        sourcePath: 'docs//old.txt',
        destinationPath: '/archive/new.txt',
      );
      final renamed = await harness.api.rename(
        logicalPath: '/archive/new.txt',
        newName: '最终 报告.txt',
      );
      final copied = await harness.api.copy(
        sourcePath: '/archive/最终 报告.txt',
        destinationPath: '/backup/最终 报告.txt',
      );

      expect(moved.statusCode, 200);
      expect(moved.data.sourcePath, '/docs/old.txt');
      expect(moved.data.destinationPath, '/archive/new.txt');
      expect(renamed.data.destinationPath, '/archive/最终 报告.txt');
      expect(copied.statusCode, 201);
      expect(copied.data.persistenceWarning, isTrue);
      expect(copied.warnings, hasLength(1));

      expect(harness.adapter.requests[0].method, 'POST');
      expect(harness.adapter.requests[0].path, '/api/v1/files-move');
      expect(harness.adapter.requests[0].data, {
        'from': '/docs/old.txt',
        'to': '/archive/new.txt',
      });
      expect(harness.adapter.requests[1].data, {
        'from': '/archive/new.txt',
        'to': '/archive/最终 报告.txt',
      });
      expect(harness.adapter.requests[2].path, '/api/v1/files-copy');
      expect(harness.adapter.requests[2].data, {
        'from': '/archive/最终 报告.txt',
        'to': '/backup/最终 报告.txt',
      });
    },
  );

  test(
    'safe delete carries one coherent intent snapshot into DELETE',
    () async {
      final harness = await _Harness.create();
      final firstIdentity = _token('1');
      final secondIdentity = _token('2');
      final entries = [
        _entry('/docs/a.txt', firstIdentity),
        _entry('/照片/家庭 相册', secondIdentity, isDirectory: true),
      ];

      final prepared = await harness.api.prepareDeleteIntent(entries);

      expect(prepared.statusCode, 200);
      expect(prepared.data.policy.mode, DeleteMode.trash);
      expect(prepared.data.policy.token, _token('a'));
      expect(prepared.data.policy.retentionDays, 30);
      expect(prepared.data.targets, hasLength(2));
      expect(prepared.data.targets[1].identityToken, secondIdentity);
      expect(prepared.data.targets[1].targetToken, _token('c'));

      final prepareRequest = harness.adapter.requests.single;
      expect(prepareRequest.path, '/api/v1/files-delete-intents');
      expect(prepareRequest.method, 'POST');
      expect(prepareRequest.data, {
        'targets': [
          {'path': '/docs/a.txt', 'observedIdentityToken': firstIdentity},
          {'path': '/照片/家庭 相册', 'observedIdentityToken': secondIdentity},
        ],
      });

      final confirmation = prepared.data.confirmationForPath('/照片/家庭 相册');
      final deleted = await harness.api.delete(confirmation);

      expect(deleted.data.path, '/照片/家庭 相册');
      expect(deleted.data.hasWarning, isTrue);
      expect(deleted.warnings, hasLength(1));
      final deleteRequest = harness.adapter.requests.last;
      expect(deleteRequest.method, 'DELETE');
      expect(
        deleteRequest.uri,
        contains(
          '/api/v1/files/%E7%85%A7%E7%89%87/'
          '%E5%AE%B6%E5%BA%AD%20%E7%9B%B8%E5%86%8C?',
        ),
      );
      expect(deleteRequest.query, {
        'expected_delete_mode': 'trash',
        'expected_delete_policy_token': _token('a'),
        'expected_delete_target_token': _token('c'),
      });
    },
  );

  test('rejects a delete intent response with a changed identity', () async {
    final harness = await _Harness.create(responseIdentityMismatch: true);

    await expectLater(
      harness.api.prepareDeleteIntent([_entry('/docs/a.txt', _token('1'))]),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'INVALID_RESPONSE',
        ),
      ),
    );
  });

  test(
    'rejects unavailable, duplicate, and nested observations locally',
    () async {
      final harness = await _Harness.create();
      final validToken = _token('1');

      expect(
        () => harness.api.prepareDeleteIntent([_entry('/docs/a.txt', null)]),
        throwsFormatException,
      );
      expect(
        () => harness.api.prepareDeleteIntent([
          _entry('/docs', validToken, isDirectory: true),
          _entry('/docs/a.txt', _token('2')),
        ]),
        throwsFormatException,
      );
      expect(
        () => harness.api.prepareDeleteIntent([
          _entry('/docs/a.txt', validToken),
          _entry('/docs/a.txt', _token('2')),
        ]),
        throwsFormatException,
      );
      expect(harness.adapter.requests, isEmpty);
    },
  );
}

FileEntry _entry(
  String path,
  String? identityToken, {
  bool isDirectory = false,
}) {
  return FileEntry(
    name: path.split('/').last,
    path: path,
    isDirectory: isDirectory,
    size: isDirectory ? 0 : 42,
    modifiedAt: DateTime.utc(2026, 7, 19, 12),
    capabilities: const FileCapabilities(
      read: true,
      concreteRead: true,
      write: true,
    ),
    deleteIdentityToken: identityToken,
  );
}

String _token(String character) => List.filled(64, character).join();

final class _Harness {
  const _Harness({required this.api, required this.adapter});

  static Future<_Harness> create({
    bool responseIdentityMismatch = false,
  }) async {
    final store = MemoryAuthSessionStore();
    await store.save(
      AuthSession(
        serverBaseUrl: 'https://nas.example.com',
        tokens: AuthTokenPair(
          accessToken: 'access-token',
          refreshToken: 'refresh-token',
          expiresAt: DateTime.utc(2026, 7, 19, 13),
        ),
      ),
    );
    final adapter = _FilesMutationAdapter(
      responseIdentityMismatch: responseIdentityMismatch,
    );
    final dio = Dio()..httpClientAdapter = adapter;
    final client = ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: dio,
    );
    return _Harness(api: FilesApi(client), adapter: adapter);
  }

  final FilesApi api;
  final _FilesMutationAdapter adapter;
}

final class _RequestRecord {
  const _RequestRecord({
    required this.method,
    required this.path,
    required this.uri,
    required this.data,
    required this.query,
  });

  final String method;
  final String path;
  final String uri;
  final Object? data;
  final Map<String, dynamic> query;
}

final class _FilesMutationAdapter implements HttpClientAdapter {
  _FilesMutationAdapter({required this.responseIdentityMismatch});

  final bool responseIdentityMismatch;
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
        data: options.data,
        query: Map<String, dynamic>.from(options.queryParameters),
      ),
    );

    return switch (options.uri.path) {
      '/api/v1/files-move' => _json(200, {
        'success': true,
        'data': options.data,
        'message': 'file moved successfully',
      }),
      '/api/v1/files-copy' => _json(201, {
        'success': true,
        'data': {...(options.data! as Map<String, dynamic>), 'warning': true},
        'message': 'resource copied with persistence warning',
      }, warning: '199 MnemoNAS "workspace mutation persistence incomplete"'),
      '/api/v1/files-delete-intents' => _prepareDelete(options),
      _ when options.method == 'DELETE' => _json(200, {
        'success': true,
        'data': {
          'path': Uri.decodeComponent(
            options.uri.path.substring('/api/v1/files'.length),
          ),
          'warning': true,
        },
        'message': 'file deleted with cleanup warning',
      }, warning: '199 MnemoNAS "delete cleanup incomplete"'),
      _ => _json(404, {'code': 'NOT_FOUND', 'message': 'not found'}),
    };
  }

  ResponseBody _prepareDelete(RequestOptions options) {
    final body = options.data! as Map<String, dynamic>;
    final targets = body['targets']! as List<dynamic>;
    return _json(200, {
      'success': true,
      'data': {
        'deleteMode': 'trash',
        'deletePolicyToken': _token('a'),
        'trashRetentionDays': 30,
        'trashAutoCleanupEnabled': true,
        'targets': [
          for (var index = 0; index < targets.length; index++)
            {
              'path': (targets[index] as Map<String, dynamic>)['path'],
              'name':
                  ((targets[index] as Map<String, dynamic>)['path'] as String)
                      .split('/')
                      .last,
              'isDir': index == 1,
              'size': index == 1 ? 0 : 42,
              'modTime': '2026-07-19T12:00:00.000000000Z',
              'deleteIdentityToken': responseIdentityMismatch
                  ? _token('f')
                  : (targets[index]
                        as Map<String, dynamic>)['observedIdentityToken'],
              'deleteTargetToken': _token(String.fromCharCode(98 + index)),
            },
        ],
      },
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
