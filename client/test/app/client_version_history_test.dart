import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/app/client_controller.dart';
import 'package:mnemonas_client/app/client_state.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/files/file_models.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';
import 'package:mnemonas_client/core/server/server_preferences.dart';
import 'package:mnemonas_client/core/transfers/file_transfer_store.dart';

void main() {
  group('version history orchestration', () {
    test('binds history reads to the file identity that opened it', () async {
      final adapter = _VersionAdapter(includeDirectoryHash: true);
      final harness = await _VersionHarness.start(adapter);
      addTearDown(harness.dispose);
      final entry = await harness.openReport();

      final history = await harness.controller.listVersionHistory(entry);
      expect(history.current.hash, adapter.initialHash);

      adapter.currentHash = _hash('c');
      await expectLater(
        harness.controller.listVersionHistory(entry),
        _throwsApiCode('VERSION_TARGET_CHANGED'),
      );
    });

    test(
      'uses the version-history current identity when listings omit hashes',
      () async {
        final adapter = _VersionAdapter();
        final harness = await _VersionHarness.start(adapter);
        addTearDown(harness.dispose);
        final entry = await harness.openReport();
        expect(entry.contentHash, isNull);

        final history = await harness.controller.listVersionHistory(entry);
        expect(history.current.hash, adapter.initialHash);

        adapter.currentHash = _hash('c');
        final refreshed = await harness.controller.listVersionHistory(entry);
        expect(refreshed.current.hash, _hash('c'));
      },
    );

    test('rejects restore locally for a non-admin account', () async {
      final adapter = _VersionAdapter(userRole: 'user');
      final harness = await _VersionHarness.start(adapter);
      addTearDown(harness.dispose);
      final entry = await harness.openReport();
      final history = await harness.controller.listVersionHistory(entry);

      await expectLater(
        harness.controller.restoreFileVersion(
          entry: entry,
          version: history.versions[1],
          expectedCurrentHash: history.current.hash,
        ),
        _throwsApiCode('ADMIN_REQUIRED'),
      );
      expect(adapter.restoreRequestCount, 0);
    });

    test('revalidates the directory identity before restore', () async {
      final adapter = _VersionAdapter();
      final harness = await _VersionHarness.start(adapter);
      addTearDown(harness.dispose);
      final entry = await harness.openReport();
      final history = await harness.controller.listVersionHistory(entry);

      adapter.currentHash = _hash('c');
      await expectLater(
        harness.controller.restoreFileVersion(
          entry: entry,
          version: history.versions[1],
          expectedCurrentHash: history.current.hash,
        ),
        _throwsApiCode('VERSION_TARGET_CHANGED'),
      );
      expect(adapter.restoreRequestCount, 0);
    });

    test('shares the file mutation single-flight guard', () async {
      final adapter = _VersionAdapter();
      final harness = await _VersionHarness.start(adapter);
      addTearDown(harness.dispose);
      final entry = await harness.openReport();
      final history = await harness.controller.listVersionHistory(entry);
      final gate = adapter.holdRestore();

      final first = harness.controller.restoreFileVersion(
        entry: entry,
        version: history.versions[1],
        expectedCurrentHash: history.current.hash,
      );
      await gate.started.timeout(const Duration(seconds: 2));

      await expectLater(
        harness.controller.restoreFileVersion(
          entry: entry,
          version: history.versions[1],
          expectedCurrentHash: history.current.hash,
        ),
        _throwsApiCode('FILE_MUTATION_IN_PROGRESS'),
      );
      expect(adapter.restoreRequestCount, 1);

      gate.release();
      final restored = await first;
      expect(restored.current.hash, adapter.historicalHash);
      expect(
        harness.container
            .read(clientControllerProvider)
            .directory
            ?.entries
            .single
            .contentHash,
        isNull,
      );
    });

    test(
      'restores in a guarded order and cautiously refreshes the directory',
      () async {
        final adapter = _VersionAdapter();
        final harness = await _VersionHarness.start(adapter);
        addTearDown(harness.dispose);
        final entry = await harness.openReport();
        final history = await harness.controller.listVersionHistory(entry);
        final marker = adapter.requests.length;

        final restored = await harness.controller.restoreFileVersion(
          entry: entry,
          version: history.versions[1],
          expectedCurrentHash: history.current.hash,
        );

        expect(restored.current.hash, adapter.historicalHash);
        expect(
          adapter.requests
              .skip(marker)
              .map((request) => '${request.method} ${request.path}'),
          orderedEquals(<String>[
            'GET /api/v1/files/docs',
            'GET /api/v1/versions/docs/report.txt',
            'POST /api/v1/versions/${adapter.historicalHash}/restore',
            'GET /api/v1/versions/docs/report.txt',
            'GET /api/v1/files/docs',
          ]),
        );
        expect(
          harness.container.read(clientControllerProvider).notice,
          '版本已恢复。',
        );
      },
    );

    test('does not blindly retry a disconnected restore', () async {
      final adapter = _VersionAdapter()..disconnectNextRestore = true;
      final harness = await _VersionHarness.start(adapter);
      addTearDown(harness.dispose);
      final entry = await harness.openReport();
      final history = await harness.controller.listVersionHistory(entry);

      await expectLater(
        harness.controller.restoreFileVersion(
          entry: entry,
          version: history.versions[1],
          expectedCurrentHash: history.current.hash,
        ),
        _throwsApiCode(versionRestoreResultUnconfirmedCode),
      );

      expect(adapter.restoreRequestCount, 1);
      expect(adapter.currentHash, adapter.historicalHash);
      final state = harness.container.read(clientControllerProvider);
      expect(state.isFileMutationBusy, isFalse);
      expect(state.errorMessage, contains('避免重复提交'));
    });

    test('treats a structured HTTP 500 restore as unconfirmed', () async {
      final adapter = _VersionAdapter()..nextRestoreStatus = 500;
      final harness = await _VersionHarness.start(adapter);
      addTearDown(harness.dispose);
      final entry = await harness.openReport();
      final history = await harness.controller.listVersionHistory(entry);

      await expectLater(
        harness.controller.restoreFileVersion(
          entry: entry,
          version: history.versions[1],
          expectedCurrentHash: history.current.hash,
        ),
        _throwsApiCode(versionRestoreResultUnconfirmedCode),
      );
      expect(adapter.restoreRequestCount, 1);
    });

    test(
      'keeps a structured HTTP 4xx restore as a definite rejection',
      () async {
        final adapter = _VersionAdapter()..nextRestoreStatus = 409;
        final harness = await _VersionHarness.start(adapter);
        addTearDown(harness.dispose);
        final entry = await harness.openReport();
        final history = await harness.controller.listVersionHistory(entry);

        await expectLater(
          harness.controller.restoreFileVersion(
            entry: entry,
            version: history.versions[1],
            expectedCurrentHash: history.current.hash,
          ),
          _throwsApiCode('CONFLICT'),
        );
        expect(adapter.restoreRequestCount, 1);
      },
    );

    test(
      'requires reopening history when its confirmed refresh fails',
      () async {
        final adapter = _VersionAdapter()..failHistoryAfterRestore = true;
        final harness = await _VersionHarness.start(adapter);
        addTearDown(harness.dispose);
        final entry = await harness.openReport();
        final history = await harness.controller.listVersionHistory(entry);

        await expectLater(
          harness.controller.restoreFileVersion(
            entry: entry,
            version: history.versions[1],
            expectedCurrentHash: history.current.hash,
          ),
          _throwsApiCode(versionRestoreConfirmedRefreshRequiredCode),
        );

        expect(adapter.restoreRequestCount, 1);
        final state = harness.container.read(clientControllerProvider);
        expect(state.errorMessage, isNull);
        expect(state.notice, contains('已确认恢复'));
        expect(state.notice, contains('不要重复恢复'));
        expect(state.directory?.entries.single.contentHash, isNull);
      },
    );

    test('downloads the exact selected historical version', () async {
      final adapter = _VersionAdapter();
      final harness = await _VersionHarness.start(adapter);
      addTearDown(harness.dispose);
      final entry = await harness.openReport();
      final history = await harness.controller.listVersionHistory(entry);
      final destination = File('${harness.directory.path}/restored.bin');

      final result = await harness.controller.downloadFileVersion(
        entry: entry,
        version: history.versions[1],
        destinationPath: destination.path,
      );

      expect(await result.readAsBytes(), adapter.historicalPayload);
      final request = adapter.requests.last;
      expect(request.path, '/api/v1/download/docs/report.txt');
      expect(request.query['version'], adapter.historicalHash);
    });

    test('cancels non-durable version downloads on background entry', () async {
      final adapter = _VersionAdapter();
      final harness = await _VersionHarness.start(adapter);
      addTearDown(harness.dispose);
      final entry = await harness.openReport();
      final history = await harness.controller.listVersionHistory(entry);
      final destination = File('${harness.directory.path}/background.bin');
      final gate = adapter.holdDownload();

      final download = harness.controller.downloadFileVersion(
        entry: entry,
        version: history.versions[1],
        destinationPath: destination.path,
      );
      await gate.started.timeout(const Duration(seconds: 2));

      expect(
        await harness.controller.pauseActiveTransfersForAppBackground(),
        1,
      );
      await expectLater(
        download,
        throwsA(
          isA<ApiException>().having(
            (error) => error.kind,
            'kind',
            ApiFailureKind.cancelled,
          ),
        ),
      );
      expect(await destination.exists(), isFalse);
      expect(
        harness.directory.listSync().whereType<File>().where(
          (file) => file.path.contains('.mnemonas-version-'),
        ),
        isEmpty,
      );
    });
  });
}

Matcher _throwsApiCode(String code) {
  return throwsA(
    isA<ApiException>().having((error) => error.code, 'code', code),
  );
}

String _hash(String character) => List<String>.filled(64, character).join();

final class _VersionHarness {
  const _VersionHarness({
    required this.container,
    required this.controller,
    required this.directory,
  });

  static Future<_VersionHarness> start(_VersionAdapter adapter) async {
    final directory = await Directory.systemTemp.createTemp(
      'mnemonas-version-controller-',
    );
    final preferences = _MemoryServerPreferences();
    final sessionStore = MemoryAuthSessionStore();
    final container = ProviderContainer(
      overrides: [
        serverPreferencesProvider.overrideWithValue(preferences),
        authSessionStoreProvider.overrideWithValue(sessionStore),
        transferStoreFactoryProvider.overrideWithValue(
          () async => FileTransferStore(directoryPath: directory.path),
        ),
        apiClientFactoryProvider.overrideWithValue((endpoint, store) {
          return ApiClient(
            endpoint: endpoint,
            sessionStore: store,
            dio: Dio()..httpClientAdapter = adapter,
          );
        }),
        clientControllerProvider.overrideWith(ClientController.new),
      ],
    );
    final controller = container.read(clientControllerProvider.notifier);
    await _waitUntil(
      () =>
          container.read(clientControllerProvider).stage ==
          ClientStage.needsConnection,
    );
    final endpoint = ServerEndpoint.parse('https://nas.example.com');
    await controller.connect(endpoint.baseUrl);
    await controller.login(username: 'owner', password: 'password');
    expect(container.read(clientControllerProvider).stage, ClientStage.ready);
    return _VersionHarness(
      container: container,
      controller: controller,
      directory: directory,
    );
  }

  final ProviderContainer container;
  final ClientController controller;
  final Directory directory;

  Future<FileEntry> openReport() async {
    await controller.loadDirectory('/docs');
    return container.read(clientControllerProvider).directory!.entries.single;
  }

  Future<void> dispose() async {
    container.dispose();
    if (await directory.exists()) {
      await directory.delete(recursive: true);
    }
  }
}

final class _MemoryServerPreferences implements ServerPreferencesStore {
  ServerEndpoint? endpoint;

  @override
  Future<ServerEndpoint?> load() async => endpoint;

  @override
  Future<void> save(
    ServerEndpoint value, {
    bool allowInsecurePublicHttp = false,
  }) async {
    endpoint = value;
  }

  @override
  Future<void> clear() async {
    endpoint = null;
  }
}

final class _RequestGate {
  final Completer<void> _started = Completer<void>();
  final Completer<void> _released = Completer<void>();

  Future<void> get started => _started.future;

  Future<void> wait() async {
    if (!_started.isCompleted) {
      _started.complete();
    }
    await _released.future;
  }

  void release() {
    if (!_released.isCompleted) {
      _released.complete();
    }
  }
}

final class _VersionAdapter implements HttpClientAdapter {
  _VersionAdapter({this.userRole = 'admin', this.includeDirectoryHash = false});

  final String userRole;
  final bool includeDirectoryHash;
  final String initialHash = _hash('a');
  final String historicalHash = _hash('b');
  final List<int> initialPayload = const <int>[1, 2, 3, 4];
  final List<int> historicalPayload = const <int>[8, 9, 10];
  final List<_RecordedRequest> requests = <_RecordedRequest>[];
  late String currentHash = initialHash;
  bool disconnectNextRestore = false;
  bool failHistoryAfterRestore = false;
  int? nextRestoreStatus;
  bool _restored = false;
  bool _failNextHistoryRead = false;
  _RequestGate? _restoreGate;
  _RequestGate? _downloadGate;

  int get restoreRequestCount => requests
      .where(
        (request) =>
            request.method == 'POST' &&
            request.path.startsWith('/api/v1/versions/'),
      )
      .length;

  _RequestGate holdRestore() {
    return _restoreGate = _RequestGate();
  }

  _RequestGate holdDownload() {
    return _downloadGate = _RequestGate();
  }

  @override
  void close({bool force = false}) {}

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.uri.path;
    requests.add(
      _RecordedRequest(
        method: options.method,
        path: path,
        query: Map<String, dynamic>.from(options.queryParameters),
      ),
    );

    if (path == '/health') {
      return _json(200, {
        'status': 'healthy',
        'timestamp': '2026-07-19T12:00:00Z',
        'uptime_secs': 3600,
        'version': 'dev',
      });
    }
    if (path == '/api/v1/version') {
      return _envelope({
        'name': 'MnemoNAS',
        'version': 'dev',
        'build_time': '2026-07-19T12:00:00Z',
        'go': 'go1.25',
      });
    }
    if (path == '/api/v1/setup/') {
      return _json(200, {
        'success': true,
        'is_first_run': false,
        'auth_enabled': true,
        'share_enabled': true,
        'webdav_enabled': true,
        'allow_unsafe_no_auth': false,
      });
    }
    if (path == '/api/v1/auth/login') {
      return _envelope({
        'access_token': 'access-owner',
        'refresh_token': 'refresh-owner',
        'expires_at': '2099-01-01T00:00:00Z',
        'token_type': 'Bearer',
        'user': _user(),
      });
    }
    if (path == '/api/v1/stats') {
      return _envelope({
        'total_files_available': true,
        'storage_stats_available': true,
        'disk_stats_available': true,
        'total_files': 1,
        'total_size': initialPayload.length,
        'unique_size': initialPayload.length,
        'dedup_ratio': 1.0,
        'disk_total': 100,
        'disk_used': 4,
        'disk_available': 96,
        'disk_usage_ratio': 0.04,
      });
    }
    if (path == '/api/v1/files/' || path == '/api/v1/files/docs') {
      return _envelope(_directory(path == '/api/v1/files/' ? '/' : '/docs'));
    }
    if (path == '/api/v1/versions/docs/report.txt' && options.method == 'GET') {
      if (_failNextHistoryRead) {
        _failNextHistoryRead = false;
        throw DioException(
          requestOptions: options,
          type: DioExceptionType.connectionError,
          error: 'version history refresh disconnected',
        );
      }
      return _envelope(_history());
    }
    if (path.startsWith('/api/v1/versions/') &&
        path.endsWith('/restore') &&
        options.method == 'POST') {
      final gate = _restoreGate;
      _restoreGate = null;
      if (gate != null) {
        await gate.wait();
      }
      final status = nextRestoreStatus;
      nextRestoreStatus = null;
      if (status != null) {
        return _json(status, {
          'code': status >= 500 ? 'INTERNAL_ERROR' : 'CONFLICT',
          'message': status >= 500
              ? 'restore state is uncertain'
              : 'file changed before restore',
        });
      }

      currentHash = historicalHash;
      _restored = true;
      if (disconnectNextRestore) {
        disconnectNextRestore = false;
        throw DioException(
          requestOptions: options,
          type: DioExceptionType.connectionError,
          error: 'restore response disconnected',
        );
      }
      if (failHistoryAfterRestore) {
        failHistoryAfterRestore = false;
        _failNextHistoryRead = true;
      }
      return _json(200, {
        'success': true,
        'data': {
          'path': '/docs/report.txt',
          'restored': historicalHash,
          'warning': false,
        },
        'message': 'version restored successfully',
      });
    }
    if (path == '/api/v1/download/docs/report.txt' && options.method == 'GET') {
      final gate = _downloadGate;
      _downloadGate = null;
      if (gate != null) {
        final cancellation = cancelFuture;
        var cancelled = false;
        await Future.any<void>(<Future<void>>[
          gate.wait(),
          if (cancellation != null)
            cancellation.then((_) {
              cancelled = true;
            }),
        ]);
        if (cancelled) {
          throw DioException(
            requestOptions: options,
            type: DioExceptionType.cancel,
            error: 'app entered the background',
          );
        }
      }
      return ResponseBody.fromBytes(
        historicalPayload,
        HttpStatus.ok,
        headers: {
          Headers.contentLengthHeader: ['${historicalPayload.length}'],
          HttpHeaders.etagHeader: ['"$historicalHash"'],
        },
      );
    }
    return _json(404, {'code': 'NOT_FOUND', 'message': 'not found'});
  }

  Map<String, dynamic> _user() {
    return {
      'id': 'user-owner',
      'username': 'owner',
      'role': userRole,
      'home_dir': '/',
      'must_change_password': false,
    };
  }

  Map<String, dynamic> _directory(String path) {
    final files = path == '/'
        ? <Map<String, dynamic>>[
            {
              'name': 'docs',
              'path': '/docs',
              'isDir': true,
              'size': 0,
              'modTime': '2026-07-19T12:00:00Z',
              'capabilities': _capabilities(),
            },
          ]
        : <Map<String, dynamic>>[
            {
              'name': 'report.txt',
              'path': '/docs/report.txt',
              'isDir': false,
              'size': _restored
                  ? historicalPayload.length
                  : initialPayload.length,
              'modTime': '2026-07-19T12:00:00Z',
              if (includeDirectoryHash) 'hash': currentHash,
              'versioned': true,
              'deleteIdentityToken': _hash('d'),
              'capabilities': _capabilities(),
            },
          ];
    return {
      'path': path,
      'capabilities': _capabilities(),
      'deleteMode': 'trash',
      'deletePolicyToken': _hash('e'),
      'trashRetentionDays': 30,
      'trashAutoCleanupEnabled': true,
      'files': files,
    };
  }

  Map<String, dynamic> _history() {
    if (_restored) {
      return {
        'path': '/docs/report.txt',
        'versions': [
          {
            'version': 1,
            'hash': historicalHash,
            'size': historicalPayload.length,
            'timestamp': '2026-07-19T13:00:00Z',
            'comment': '(current)',
          },
          {
            'version': 2,
            'hash': initialHash,
            'size': initialPayload.length,
            'timestamp': '2026-07-19T12:00:00Z',
            'comment': 'before restore',
          },
        ],
      };
    }
    return {
      'path': '/docs/report.txt',
      'versions': [
        {
          'version': 1,
          'hash': currentHash,
          'size': initialPayload.length,
          'timestamp': '2026-07-19T12:00:00Z',
          'comment': '(current)',
        },
        {
          'version': 2,
          'hash': historicalHash,
          'size': historicalPayload.length,
          'timestamp': '2026-07-18T12:00:00Z',
          'comment': 'automatic',
        },
      ],
    };
  }

  Map<String, dynamic> _capabilities() {
    return {'read': true, 'concreteRead': true, 'write': true};
  }

  ResponseBody _envelope(Object data) {
    return _json(200, {'success': true, 'data': data});
  }

  ResponseBody _json(int statusCode, Object body) {
    return ResponseBody.fromString(
      jsonEncode(body),
      statusCode,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }
}

final class _RecordedRequest {
  const _RecordedRequest({
    required this.method,
    required this.path,
    required this.query,
  });

  final String method;
  final String path;
  final Map<String, dynamic> query;
}

Future<void> _waitUntil(bool Function() condition) async {
  for (var attempt = 0; attempt < 100; attempt++) {
    if (condition()) {
      return;
    }
    await Future<void>.delayed(const Duration(milliseconds: 1));
  }
  fail('Condition was not met before the timeout');
}
