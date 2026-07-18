import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:dio/dio.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/app/client_controller.dart';
import 'package:mnemonas_client/app/client_state.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/files/file_models.dart';
import 'package:mnemonas_client/core/files/files_api.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/search/search_models.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';
import 'package:mnemonas_client/core/server/server_preferences.dart';
import 'package:mnemonas_client/core/transfers/file_transfer_store.dart';
import 'package:mnemonas_client/core/transfers/transfer_task.dart'
    as transfer_core;
import 'package:mnemonas_client/core/trash/trash_models.dart';

void main() {
  test('native storage failures use actionable client messages', () {
    expect(
      clientErrorMessage(PlatformException(code: 'IMPORT_CANCELLED')),
      '操作已取消。',
    );
    expect(
      clientErrorMessage(PlatformException(code: 'EXPORT_FAILED')),
      contains('目标权限'),
    );
    expect(
      clientErrorMessage(PlatformException(code: 'IMPORT_TOO_LARGE')),
      contains('10 GiB'),
    );
    expect(
      clientErrorMessage(
        const ApiException(
          kind: ApiFailureKind.local,
          code: 'UPLOAD_SOURCE_TOO_LARGE',
          message: 'source exceeds the limit',
        ),
      ),
      contains('10 GiB'),
    );
    expect(
      clientErrorMessage(
        const ApiException(
          kind: ApiFailureKind.response,
          statusCode: 507,
          code: 'UPLOAD_STAGING_CAPACITY_EXCEEDED',
          message: 'staging capacity exhausted',
        ),
      ),
      contains('可用空间不足'),
    );
  });

  group('terminal authentication failures', () {
    const terminalCodes = <String>[
      'AUTH_SESSION_MISSING',
      'AUTH_CONTEXT_CHANGED',
      'AUTH_SESSION_STORAGE_FAILED',
      'AUTH_SCOPE_CHANGED',
      'INVALID_AUTH_HEADER',
      'INVALID_TOKEN',
      'MISSING_AUTH_HEADER',
      'NOT_AUTHENTICATED',
      'TOKEN_EXPIRED',
      'TOKEN_REVOKED',
      'USER_DISABLED',
      'USER_NOT_FOUND',
    ];

    for (final code in terminalCodes) {
      test('$code clears the session and returns to login', () async {
        final store = _TrackingSessionStore(_session());
        final container = ProviderContainer(
          overrides: [
            authSessionStoreProvider.overrideWithValue(store),
            clientControllerProvider.overrideWith(_ReadyController.new),
          ],
        );
        addTearDown(container.dispose);

        final controller = container.read(clientControllerProvider.notifier);
        final handled = await controller.handleAuthenticationFailure(
          ApiException(
            kind: ApiFailureKind.response,
            statusCode: code == 'USER_DISABLED' ? 403 : 401,
            code: code,
            message: 'authentication rejected',
          ),
        );

        expect(handled, isTrue);
        expect(store.clearCount, 1);
        expect((await store.snapshot()).session, isNull);

        final state = container.read(clientControllerProvider);
        expect(state.stage, ClientStage.needsLogin);
        expect(state.endpoint?.baseUrl, 'https://nas.example.com');
        expect(state.user, isNull);
        expect(state.currentPath, '/');
        expect(state.directory, isNull);
        expect(state.stats, isNull);
        expect(state.transfers, isEmpty);
        expect(state.isBusy, isFalse);
        expect(state.notice, isNull);
        expect(state.errorMessage, '登录状态已失效，请重新登录。');
      });
    }
  });

  test(
    'recoverable network and service errors keep the signed-in state',
    () async {
      final recoverableErrors = <ApiException>[
        const ApiException(
          kind: ApiFailureKind.connection,
          code: 'CONNECTION_FAILED',
          message: 'network unavailable',
        ),
        const ApiException(
          kind: ApiFailureKind.timeout,
          code: 'REQUEST_TIMEOUT',
          message: 'request timed out',
        ),
        const ApiException(
          kind: ApiFailureKind.response,
          statusCode: 503,
          code: 'TOKEN_STATE_UNAVAILABLE',
          message: 'token state unavailable',
        ),
        const ApiException(
          kind: ApiFailureKind.response,
          statusCode: 401,
          code: 'INVALID_PASSWORD',
          message: 'current password is incorrect',
        ),
      ];

      for (final error in recoverableErrors) {
        final store = _TrackingSessionStore(_session());
        final container = ProviderContainer(
          overrides: [
            authSessionStoreProvider.overrideWithValue(store),
            clientControllerProvider.overrideWith(_ReadyController.new),
          ],
        );

        final controller = container.read(clientControllerProvider.notifier);
        final handled = await controller.handleAuthenticationFailure(error);

        expect(handled, isFalse, reason: error.code);
        expect(store.clearCount, 0, reason: error.code);
        expect((await store.snapshot()).session, isNotNull, reason: error.code);
        expect(
          container.read(clientControllerProvider).stage,
          ClientStage.ready,
          reason: error.code,
        );
        container.dispose();
      }
    },
  );

  group('context epoch isolation', () {
    test('a delayed login is superseded by a later login', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);

      await harness.controller.connect(endpoint.baseUrl);
      final delayedLogin = adapter.holdLogin('alice');
      addTearDown(delayedLogin.release);

      final loginA = harness.controller.login(
        username: 'alice',
        password: 'password-a',
      );
      await delayedLogin.started.timeout(const Duration(seconds: 2));

      await harness.controller.login(username: 'bob', password: 'password-b');
      var state = harness.container.read(clientControllerProvider);
      expect(state.stage, ClientStage.ready);
      expect(state.user?.username, 'bob');

      delayedLogin.release();
      await loginA;

      state = harness.container.read(clientControllerProvider);
      expect(state.stage, ClientStage.ready);
      expect(state.user?.username, 'bob');
      expect(state.endpoint, endpoint);
      final session = (await harness.sessionStore.snapshot()).session;
      expect(session?.tokens.accessToken, 'access-bob');
      expect(session?.tokens.refreshToken, 'refresh-bob');
    });

    test(
      'a new account cannot observe trash cached by the previous account',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);

        await harness.connectAndLogin(endpoint, username: 'alice');
        await harness.controller.loadTrash();
        expect(
          harness.container.read(clientControllerProvider).trash?.count,
          3,
        );

        await harness.controller.changePassword(
          currentPassword: 'password',
          newPassword: 'new-password',
        );
        var state = harness.container.read(clientControllerProvider);
        expect(state.stage, ClientStage.needsLogin);
        expect(state.user, isNull);
        expect(state.trash, isNull);

        adapter.trashItems
          ..clear()
          ..add(_trashItem(id: 'trash-bob', path: '/bob/private.txt', size: 7));
        await harness.controller.login(
          username: 'bob',
          password: 'new-password',
        );

        state = harness.container.read(clientControllerProvider);
        expect(state.user?.username, 'bob');
        expect(state.trash, isNull);
        await harness.controller.loadTrash();
        expect(
          harness.container
              .read(clientControllerProvider)
              .trash
              ?.items
              .map((item) => item.originalPath),
          orderedEquals(const <String>['/bob/private.txt']),
        );
      },
    );

    test(
      'password completion fences a pending refresh lease and stale reads',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final sessionStore = _GatedCommitSessionStore();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
          sessionStore: sessionStore,
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'alice');

        final passwordGate = adapter.holdPasswordChange();
        final passwordChange = harness.controller.changePassword(
          currentPassword: 'password',
          newPassword: 'new-password',
        );
        await passwordGate.started.timeout(const Duration(seconds: 2));

        final trashGate = adapter.holdTrashList();
        final directoryGate = adapter.holdDirectory('/private');
        final staleTrash = harness.controller.loadTrash();
        final staleDirectory = harness.controller.loadDirectory('/private');
        await Future.wait([
          trashGate.started.timeout(const Duration(seconds: 2)),
          directoryGate.started.timeout(const Duration(seconds: 2)),
        ]);

        final activeSession = await harness.sessionStore.snapshot();
        final refreshLease = await harness.sessionStore.takeAndClearIfRevision(
          activeSession.revision,
        );
        expect(refreshLease, isNotNull);
        final refreshCommitGate = sessionStore.holdNextCommit();
        addTearDown(refreshCommitGate.release);
        final delayedRefreshCommit = harness.sessionStore.commitIfRevision(
          refreshLease!.revision,
          AuthSession(
            serverBaseUrl: endpoint.baseUrl,
            tokens: AuthTokenPair(
              accessToken: 'access-rotated',
              refreshToken: 'refresh-rotated',
              expiresAt: DateTime.utc(2026, 7, 20, 14),
            ),
          ),
        );
        await refreshCommitGate.started.timeout(const Duration(seconds: 2));

        passwordGate.release();
        await passwordChange;
        refreshCommitGate.release();
        expect(await delayedRefreshCommit, isFalse);
        trashGate.release();
        directoryGate.release();
        await Future.wait([staleTrash, staleDirectory]);

        final state = harness.container.read(clientControllerProvider);
        expect(state.stage, ClientStage.needsLogin);
        expect(state.user, isNull);
        expect(state.directory, isNull);
        expect(state.trash, isNull);
        expect(state.stats, isNull);
        expect((await harness.sessionStore.snapshot()).session, isNull);
      },
    );

    test(
      'password success remains explicit when durable session cleanup fails',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final sessionStore = _FailingFenceSessionStore();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: _ControllerAdapter()},
          sessionStore: sessionStore,
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'alice');
        sessionStore.failNextConditionalClear = true;

        await harness.controller.changePassword(
          currentPassword: 'password',
          newPassword: 'new-password',
        );

        final state = harness.container.read(clientControllerProvider);
        expect(state.stage, ClientStage.needsLogin);
        expect(state.user, isNull);
        expect(state.errorMessage, contains('密码已修改'));
        expect(state.errorMessage, contains('无法确认本机登录信息已持久清除'));
        expect((await sessionStore.snapshot()).session, isNotNull);
      },
    );

    test(
      'unconfirmed password outcome is not replaced by cleanup failure',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final sessionStore = _FailingFenceSessionStore();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
          sessionStore: sessionStore,
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'alice');
        adapter.disconnectNextPasswordChange = true;
        sessionStore.failNextConditionalClear = true;

        await expectLater(
          harness.controller.changePassword(
            currentPassword: 'password',
            newPassword: 'new-password',
          ),
          throwsA(
            isA<ApiException>().having(
              (error) => error.kind,
              'kind',
              ApiFailureKind.connection,
            ),
          ),
        );

        final state = harness.container.read(clientControllerProvider);
        expect(state.stage, ClientStage.needsLogin);
        expect(state.user, isNull);
        expect(state.errorMessage, contains('无法确认密码修改结果'));
        expect(state.errorMessage, contains('无法确认本机登录信息已持久清除'));
        expect((await sessionStore.snapshot()).session, isNotNull);
      },
    );

    test(
      'a rejected password change does not cancel an in-flight trash mutation',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'alice');
        await harness.controller.loadTrash();

        final selection = TrashSelectionSnapshot.fromItems(
          harness.container.read(clientControllerProvider).trash!.items.take(1),
        );
        final mutationGate = adapter.holdTrashMutation();
        addTearDown(mutationGate.release);
        final mutation = harness.controller.deleteTrashSelection(selection);
        await mutationGate.started.timeout(const Duration(seconds: 2));
        final directoryGate = adapter.holdDirectory('/pending');
        addTearDown(directoryGate.release);
        final directoryLoad = harness.controller.loadDirectory('/pending');
        await directoryGate.started.timeout(const Duration(seconds: 2));

        adapter.rejectNextPasswordChange = true;
        await expectLater(
          harness.controller.changePassword(
            currentPassword: 'wrong-password',
            newPassword: 'new-password',
          ),
          throwsA(
            isA<ApiException>()
                .having((error) => error.code, 'code', 'INVALID_PASSWORD')
                .having(
                  (error) => error.hasStructuredServerError,
                  'hasStructuredServerError',
                  isTrue,
                ),
          ),
        );

        var state = harness.container.read(clientControllerProvider);
        expect(state.stage, ClientStage.ready);
        expect(state.user?.username, 'alice');
        expect(state.isBusy, isTrue);
        expect(state.isTrashBusy, isTrue);
        expect(state.errorMessage, '用户名或密码不正确。');

        mutationGate.release();
        final outcome = await mutation;
        expect(outcome.deletedIds, const <String>['trash-a']);
        directoryGate.release();
        await directoryLoad;
        state = harness.container.read(clientControllerProvider);
        expect(state.stage, ClientStage.ready);
        expect(state.isBusy, isFalse);
        expect(state.isTrashBusy, isFalse);
        expect(state.currentPath, '/pending');
        expect(
          state.trash?.items.map((item) => item.id),
          orderedEquals(const <String>['trash-b', 'trash-c']),
        );
      },
    );

    test(
      'a directory response from the old endpoint cannot update the new context',
      () async {
        final oldEndpoint = ServerEndpoint.parse('https://old.example.com');
        final newEndpoint = ServerEndpoint.parse('https://new.example.com');
        final oldAdapter = _ControllerAdapter();
        final newAdapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {
            oldEndpoint.baseUrl: oldAdapter,
            newEndpoint.baseUrl: newAdapter,
          },
        );
        addTearDown(harness.container.dispose);

        await harness.connectAndLogin(oldEndpoint, username: 'owner');
        final delayedDirectory = oldAdapter.holdDirectory('/legacy');
        addTearDown(delayedDirectory.release);
        final oldLoad = harness.controller.loadDirectory('/legacy');
        await delayedDirectory.started.timeout(const Duration(seconds: 2));

        await harness.controller.connect(newEndpoint.baseUrl);
        expect(
          harness.container.read(clientControllerProvider).stage,
          ClientStage.needsLogin,
        );

        delayedDirectory.release();
        await oldLoad;

        final state = harness.container.read(clientControllerProvider);
        expect(state.stage, ClientStage.needsLogin);
        expect(state.endpoint, newEndpoint);
        expect(state.currentPath, '/');
        expect(state.directory, isNull);
        expect(state.user, isNull);
      },
    );

    test('a stale change-server clear cannot erase a newer endpoint', () async {
      final oldEndpoint = ServerEndpoint.parse('https://old.example.com');
      final newEndpoint = ServerEndpoint.parse('https://new.example.com');
      final delayedStore = _DelayedFirstClearSessionStore();
      addTearDown(delayedStore.release);
      final harness = await _ControllerHarness.start(
        adapters: {
          oldEndpoint.baseUrl: _ControllerAdapter(),
          newEndpoint.baseUrl: _ControllerAdapter(),
        },
        savedEndpoint: oldEndpoint,
        waitForBootstrap: false,
        sessionStore: delayedStore,
      );
      addTearDown(harness.container.dispose);
      await _waitUntil(
        () =>
            harness.container.read(clientControllerProvider).stage ==
            ClientStage.needsLogin,
      );

      final staleChange = harness.controller.changeServer();
      await delayedStore.firstClearStarted.timeout(const Duration(seconds: 2));
      await harness.controller.connect(newEndpoint.baseUrl);
      expect(
        harness.container.read(clientControllerProvider).endpoint,
        newEndpoint,
      );

      delayedStore.release();
      await staleChange;

      expect(harness.preferences.endpoint, newEndpoint);
      final state = harness.container.read(clientControllerProvider);
      expect(state.stage, ClientStage.needsLogin);
      expect(state.endpoint, newEndpoint);
    });

    test(
      'reverse directory completion on one endpoint keeps the latest path',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'owner');

        final delayedA = adapter.holdDirectory('/a');
        final delayedB = adapter.holdDirectory('/b');
        addTearDown(delayedA.release);
        addTearDown(delayedB.release);

        final loadA = harness.controller.loadDirectory('/a');
        await delayedA.started.timeout(const Duration(seconds: 2));
        final loadB = harness.controller.loadDirectory('/b');
        await delayedB.started.timeout(const Duration(seconds: 2));

        delayedB.release();
        await loadB;
        delayedA.release();
        await loadA;

        final state = harness.container.read(clientControllerProvider);
        expect(state.stage, ClientStage.ready);
        expect(state.endpoint, endpoint);
        expect(state.currentPath, '/b');
        expect(state.directory?.path, '/b');
        expect(state.directory?.entries.single.name, 'entry-b');
        expect(state.isBusy, isFalse);
      },
    );

    test('a delayed probe cannot write state after disposal', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter();
      final delayedProbe = adapter.holdProbe();
      addTearDown(delayedProbe.release);
      late _RecordingController recordingController;
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
        savedEndpoint: endpoint,
        waitForBootstrap: false,
        controllerFactory: () {
          return recordingController = _RecordingController();
        },
      );

      await delayedProbe.started.timeout(const Duration(seconds: 2));
      final updatesBeforeDisposal = recordingController.updateCount;
      final stateBeforeDisposal = harness.container.read(
        clientControllerProvider,
      );
      expect(stateBeforeDisposal.endpoint, endpoint);
      expect(stateBeforeDisposal.isBusy, isTrue);

      harness.container.dispose();
      delayedProbe.release();
      await delayedProbe.finished.timeout(const Duration(seconds: 2));
      await Future<void>.delayed(Duration.zero);
      await Future<void>.delayed(Duration.zero);

      expect(recordingController.updateCount, updatesBeforeDisposal);
    });
  });

  group('home freshness and file mutation safety', () {
    test(
      'an initial directory failure remains an honest ready error',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.controller.connect(endpoint.baseUrl);
        adapter.disconnectNextDirectory = true;

        await expectLater(
          harness.controller.login(username: 'owner', password: 'password'),
          throwsA(isA<ApiException>()),
        );

        final state = harness.container.read(clientControllerProvider);
        expect(state.stage, ClientStage.ready);
        expect(state.directory, isNull);
        expect(state.isDirectoryBusy, isFalse);
        expect(state.directoryErrorMessage, isNotNull);
      },
    );

    test(
      'cached directory becomes stale and recovers on a later refresh',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'owner');
        final before = harness.container.read(clientControllerProvider);
        expect(before.directory, isNotNull);
        expect(before.directoryUpdatedAt, isNotNull);

        adapter.disconnectNextDirectory = true;
        await expectLater(
          harness.controller.refreshOverview(),
          throwsA(isA<ApiException>()),
        );

        var state = harness.container.read(clientControllerProvider);
        expect(state.directory, before.directory);
        expect(state.directoryUpdatedAt, before.directoryUpdatedAt);
        expect(state.directoryErrorMessage, isNotNull);
        expect(state.isDirectoryBusy, isFalse);

        final mutationsBefore = adapter.requests
            .where(
              (request) =>
                  request.method == 'POST' &&
                  request.path.startsWith('/api/v1/directories/'),
            )
            .length;
        await expectLater(
          harness.controller.createDirectory('blocked-while-stale'),
          throwsA(
            isA<ApiException>().having(
              (error) => error.code,
              'code',
              'DIRECTORY_REFRESH_REQUIRED',
            ),
          ),
        );
        expect(
          adapter.requests.where(
            (request) =>
                request.method == 'POST' &&
                request.path.startsWith('/api/v1/directories/'),
          ),
          hasLength(mutationsBefore),
        );

        await harness.controller.refreshOverview();
        state = harness.container.read(clientControllerProvider);
        expect(state.directory?.path, '/');
        expect(state.directoryErrorMessage, isNull);
        expect(state.isDirectoryBusy, isFalse);
        expect(
          state.directoryUpdatedAt!.isBefore(before.directoryUpdatedAt!),
          isFalse,
        );
      },
    );

    test(
      'optional statistics failure does not mark file access stale',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'owner');
        adapter.failNextStats = true;

        await harness.controller.refreshOverview();

        final state = harness.container.read(clientControllerProvider);
        expect(state.stage, ClientStage.ready);
        expect(state.directory, isNotNull);
        expect(state.directoryErrorMessage, isNull);
        expect(state.stats, isNull);
      },
    );

    test(
      'file mutations are single-flight and do not restore an old path',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'owner');
        final mutationGate = adapter.holdFileMutation();
        addTearDown(mutationGate.release);

        final first = harness.controller.createDirectory('first');
        await mutationGate.started.timeout(const Duration(seconds: 2));
        expect(
          harness.container.read(clientControllerProvider).isFileMutationBusy,
          isTrue,
        );
        await expectLater(
          harness.controller.createDirectory('second'),
          throwsA(
            isA<ApiException>().having(
              (error) => error.code,
              'code',
              'FILE_MUTATION_IN_PROGRESS',
            ),
          ),
        );

        await harness.controller.loadDirectory('/other');
        mutationGate.release();
        await first;

        final state = harness.container.read(clientControllerProvider);
        expect(state.currentPath, '/other');
        expect(state.directory?.path, '/other');
        expect(state.isFileMutationBusy, isFalse);
        expect(state.notice, contains('原目录未自动刷新'));
        expect(
          adapter.requests.where(
            (request) =>
                request.method == 'POST' &&
                request.path.startsWith('/api/v1/directories/'),
          ),
          hasLength(1),
        );
      },
    );

    test('a confirmed mutation survives a failed directory refresh', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      final before = harness.container.read(clientControllerProvider).directory;
      adapter.disconnectNextDirectory = true;

      await harness.controller.createDirectory('confirmed');

      final state = harness.container.read(clientControllerProvider);
      expect(state.directory, before);
      expect(state.directoryErrorMessage, isNotNull);
      expect(state.isFileMutationBusy, isFalse);
      expect(state.notice, contains('文件夹已创建'));
      expect(state.notice, contains('内容可能已过期'));
      await expectLater(
        harness.controller.createDirectory('blocked-until-refresh'),
        throwsA(
          isA<ApiException>().having(
            (error) => error.code,
            'code',
            'DIRECTORY_REFRESH_REQUIRED',
          ),
        ),
      );
    });

    test(
      'an unconfirmed mutation blocks writes until every affected path is reviewed',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter()..disconnectNextFileMutation = true;
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'owner');

        await expectLater(
          harness.controller.createDirectory('uncertain'),
          throwsA(isA<ApiException>()),
        );
        var state = harness.container.read(clientControllerProvider);
        expect(state.fileReconciliationRequired, isTrue);
        expect(state.fileReconciliationMessage, contains('/'));
        expect(state.errorMessage, contains('结果无法确认'));
        final mutationCount = adapter.requests
            .where(
              (request) =>
                  request.method == 'POST' &&
                  request.path.startsWith('/api/v1/directories/'),
            )
            .length;

        await expectLater(
          harness.controller.createDirectory('must-not-repeat'),
          throwsA(
            isA<ApiException>().having(
              (error) => error.code,
              'code',
              'FILE_RECONCILIATION_REQUIRED',
            ),
          ),
        );
        expect(
          adapter.requests.where(
            (request) =>
                request.method == 'POST' &&
                request.path.startsWith('/api/v1/directories/'),
          ),
          hasLength(mutationCount),
        );

        await harness.controller.loadDirectory('/');
        state = harness.container.read(clientControllerProvider);
        expect(state.fileReconciliationRequired, isFalse);
        expect(state.fileReconciliationMessage, isNull);
        await harness.controller.createDirectory('safe-after-review');
      },
    );

    test('a structured server error also requires reconciliation', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter()..nextFileMutationStatus = 500;
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');

      await expectLater(
        harness.controller.createDirectory('server-uncertain'),
        throwsA(
          isA<ApiException>().having(
            (error) => error.statusCode,
            'status',
            500,
          ),
        ),
      );
      final state = harness.container.read(clientControllerProvider);
      expect(state.fileReconciliationRequired, isTrue);
      expect(state.errorMessage, contains('结果无法确认'));
    });
  });

  test(
    'disconnected upload resumes from server durable offset after restart',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter()..disconnectNextUpload = true;
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-upload-resume-',
      );
      addTearDown(() => root.delete(recursive: true));
      final transferDirectory = Directory('${root.path}/store');
      final first = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
        transferDirectory: transferDirectory,
      );
      var firstDisposed = false;
      addTearDown(() {
        if (!firstDisposed) {
          first.container.dispose();
        }
      });
      final source = File('${root.path}/payload.bin');
      await source.writeAsBytes(<int>[1, 2, 3], flush: true);
      await first.connectAndLogin(endpoint, username: 'owner');

      await expectLater(
        first.controller.uploadFile(
          sourcePath: source.path,
          fileName: 'payload.bin',
          targetDirectory: '/',
        ),
        throwsA(isA<ApiException>()),
      );

      final interrupted = first.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(interrupted.status, TransferStatus.paused);
      expect(interrupted.canRetry, isTrue);
      expect(interrupted.transferred, 0);
      expect(adapter.uploadSessions.single.durableOffset, 3);
      expect(adapter.uploadSessions.single.state, 'ready');

      first.container.dispose();
      firstDisposed = true;
      await source.delete();

      final second = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
        transferDirectory: transferDirectory,
      );
      addTearDown(second.container.dispose);
      await second.connectAndLogin(endpoint, username: 'owner');

      final restored = second.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(restored.id, interrupted.id);
      expect(restored.status, TransferStatus.paused);
      expect(restored.canRetry, isTrue);
      expect(restored.transferred, 0);

      await second.controller.resumeTransfer(restored.id);

      final completed = second.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(completed.status, TransferStatus.completed);
      expect(completed.transferred, 3);
      expect(adapter.uploadSessions.single.state, 'committed');
      final payloadDirectory = Directory(
        '${transferDirectory.path}${Platform.pathSeparator}payloads',
      );
      expect(
        payloadDirectory.listSync().whereType<File>().where(
          (file) => file.path.endsWith('.upload'),
        ),
        isEmpty,
      );
    },
  );

  test('an upload batch keeps its explicit target after navigation', () async {
    final endpoint = ServerEndpoint.parse('https://nas.example.com');
    final adapter = _ControllerAdapter();
    final root = await Directory.systemTemp.createTemp(
      'mnemonas-controller-upload-target-',
    );
    addTearDown(() => root.delete(recursive: true));
    final harness = await _ControllerHarness.start(
      adapters: {endpoint.baseUrl: adapter},
      transferDirectory: Directory('${root.path}/store'),
    );
    addTearDown(harness.container.dispose);
    final source = File('${root.path}/payload.bin');
    await source.writeAsBytes(<int>[1, 2, 3], flush: true);
    await harness.connectAndLogin(endpoint, username: 'owner');
    await harness.controller.loadDirectory('/A');
    final gate = adapter.holdUploadCreate();
    addTearDown(gate.release);

    final first = harness.controller.uploadFile(
      sourcePath: source.path,
      fileName: 'first.bin',
      targetDirectory: '/A',
    );
    await gate.started.timeout(const Duration(seconds: 2));
    await harness.controller.loadDirectory('/B');
    gate.release();
    await first;
    await harness.controller.uploadFile(
      sourcePath: source.path,
      fileName: 'second.bin',
      targetDirectory: '/A',
    );

    expect(
      adapter.uploadSessions.map((session) => session.path),
      unorderedEquals(<String>['/A/first.bin', '/A/second.bin']),
    );
    final state = harness.container.read(clientControllerProvider);
    expect(state.currentPath, '/B');
    expect(state.directory?.path, '/B');
  });

  test('a lost create response is recovered by lookup after restart', () async {
    final endpoint = ServerEndpoint.parse('https://nas.example.com');
    final adapter = _ControllerAdapter()..disconnectNextUploadCreate = true;
    final root = await Directory.systemTemp.createTemp(
      'mnemonas-controller-upload-create-recovery-',
    );
    addTearDown(() => root.delete(recursive: true));
    final transferDirectory = Directory('${root.path}/store');
    final first = await _ControllerHarness.start(
      adapters: {endpoint.baseUrl: adapter},
      transferDirectory: transferDirectory,
    );
    var firstDisposed = false;
    addTearDown(() {
      if (!firstDisposed) {
        first.container.dispose();
      }
    });
    final source = File('${root.path}/payload.bin');
    await source.writeAsBytes(<int>[1, 2, 3], flush: true);
    await first.connectAndLogin(endpoint, username: 'owner');

    await expectLater(
      first.controller.uploadFile(
        sourcePath: source.path,
        fileName: 'payload.bin',
        targetDirectory: '/',
      ),
      throwsA(isA<ApiException>()),
    );

    final interrupted = first.container
        .read(clientControllerProvider)
        .transfers
        .single;
    final persisted = (await FileTransferStore(
      directoryPath: transferDirectory.path,
    ).load()).tasks.single;
    expect(interrupted.status, TransferStatus.paused);
    expect(persisted.uploadSessionCreateAttempted, isTrue);
    expect(persisted.uploadSessionId, isNull);
    expect(adapter.uploadSessions, hasLength(1));

    first.container.dispose();
    firstDisposed = true;
    await source.delete();

    final second = await _ControllerHarness.start(
      adapters: {endpoint.baseUrl: adapter},
      transferDirectory: transferDirectory,
    );
    addTearDown(second.container.dispose);
    await second.connectAndLogin(endpoint, username: 'owner');
    final restored = second.container
        .read(clientControllerProvider)
        .transfers
        .single;

    await second.controller.resumeTransfer(restored.id);

    final completed = second.container
        .read(clientControllerProvider)
        .transfers
        .single;
    expect(completed.status, TransferStatus.completed);
    expect(
      adapter.requests.where(
        (request) =>
            request.method == 'POST' &&
            request.path == '/api/v1/upload-sessions',
      ),
      hasLength(1),
    );
    expect(
      adapter.requests.where(
        (request) =>
            request.method == 'GET' &&
            request.path.startsWith(
              '/api/v1/upload-sessions/by-client-request/',
            ),
      ),
      hasLength(1),
    );
  });

  for (final goneStatus in <int>[404, 410]) {
    test('a create lookup $goneStatus never posts another session', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter()..disconnectNextUploadCreate = true;
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-upload-create-lookup-gone-',
      );
      addTearDown(() => root.delete(recursive: true));
      final transferDirectory = Directory('${root.path}/store');
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
        transferDirectory: transferDirectory,
      );
      addTearDown(harness.container.dispose);
      final source = File('${root.path}/payload.bin');
      await source.writeAsBytes(<int>[1, 2, 3], flush: true);
      await harness.connectAndLogin(endpoint, username: 'owner');

      await expectLater(
        harness.controller.uploadFile(
          sourcePath: source.path,
          fileName: 'payload.bin',
          targetDirectory: '/',
        ),
        throwsA(isA<ApiException>()),
      );
      final interrupted = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      adapter.goneNextUploadLookupStatus = goneStatus;

      await expectLater(
        harness.controller.resumeTransfer(interrupted.id),
        throwsA(
          isA<ApiException>().having(
            (error) => error.code,
            'code',
            'UPLOAD_RESULT_UNCONFIRMED',
          ),
        ),
      );

      final task = (await FileTransferStore(
        directoryPath: transferDirectory.path,
      ).load()).tasks.single;
      expect(task.phase, transfer_core.TransferPhase.resultUnconfirmed);
      expect(task.uploadSessionCreateAttempted, isTrue);
      expect(task.uploadSessionId, isNull);
      expect(
        adapter.requests.where(
          (request) =>
              request.method == 'POST' &&
              request.path == '/api/v1/upload-sessions',
        ),
        hasLength(1),
      );
      expect(
        adapter.requests.where(
          (request) =>
              request.method == 'GET' &&
              request.path.startsWith(
                '/api/v1/upload-sessions/by-client-request/',
              ),
        ),
        hasLength(1),
      );
    });
  }

  test('a first create 410 is immediately result unconfirmed', () async {
    final endpoint = ServerEndpoint.parse('https://nas.example.com');
    final adapter = _ControllerAdapter()..expireNextUploadCreate = true;
    final root = await Directory.systemTemp.createTemp(
      'mnemonas-controller-upload-create-expired-',
    );
    addTearDown(() => root.delete(recursive: true));
    final transferDirectory = Directory('${root.path}/store');
    final harness = await _ControllerHarness.start(
      adapters: {endpoint.baseUrl: adapter},
      transferDirectory: transferDirectory,
    );
    addTearDown(harness.container.dispose);
    final source = File('${root.path}/payload.bin');
    await source.writeAsBytes(<int>[1, 2, 3], flush: true);
    await harness.connectAndLogin(endpoint, username: 'owner');

    await expectLater(
      harness.controller.uploadFile(
        sourcePath: source.path,
        fileName: 'payload.bin',
        targetDirectory: '/',
      ),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'UPLOAD_RESULT_UNCONFIRMED',
        ),
      ),
    );

    final task = (await FileTransferStore(
      directoryPath: transferDirectory.path,
    ).load()).tasks.single;
    expect(task.phase, transfer_core.TransferPhase.resultUnconfirmed);
    expect(task.uploadSessionCreateAttempted, isTrue);
    expect(task.uploadSessionId, isNull);
    expect(adapter.uploadSessions, isEmpty);
    expect(
      adapter.requests.where(
        (request) =>
            request.method == 'POST' &&
            request.path == '/api/v1/upload-sessions',
      ),
      hasLength(1),
    );
    expect(
      adapter.requests.where(
        (request) => request.path.startsWith(
          '/api/v1/upload-sessions/by-client-request/',
        ),
      ),
      isEmpty,
    );
  });

  test(
    'a confirmed upload stays completed when directory refresh fails',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      final directory = await Directory.systemTemp.createTemp(
        'mnemonas-controller-upload-refresh-',
      );
      addTearDown(() => directory.delete(recursive: true));
      final source = File('${directory.path}/payload.bin');
      await source.writeAsBytes(<int>[1, 2, 3], flush: true);
      await harness.connectAndLogin(endpoint, username: 'owner');
      adapter.disconnectNextDirectory = true;

      await harness.controller.uploadFile(
        sourcePath: source.path,
        fileName: 'payload.bin',
        targetDirectory: '/',
      );

      final state = harness.container.read(clientControllerProvider);
      final transfer = state.transfers.single;
      expect(transfer.status, TransferStatus.completed);
      expect(transfer.errorMessage, isNull);
      expect(state.errorMessage, isNull);
      expect(state.notice, contains('文件已确认上传'));
      expect(state.notice, contains('目录刷新失败'));
    },
  );

  test(
    'a missing known upload session is not recreated and can be removed locally',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter()..disconnectNextUpload = true;
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-upload-missing-session-',
      );
      addTearDown(() => root.delete(recursive: true));
      final transferDirectory = Directory('${root.path}/store');
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
        transferDirectory: transferDirectory,
      );
      addTearDown(harness.container.dispose);
      final source = File('${root.path}/payload.bin');
      await source.writeAsBytes(<int>[1, 2, 3], flush: true);
      await harness.connectAndLogin(endpoint, username: 'owner');

      await expectLater(
        harness.controller.uploadFile(
          sourcePath: source.path,
          fileName: 'payload.bin',
          targetDirectory: '/',
        ),
        throwsA(isA<ApiException>()),
      );
      final interrupted = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      adapter.forgetUploadSessions();

      await expectLater(
        harness.controller.resumeTransfer(interrupted.id),
        throwsA(
          isA<ApiException>().having(
            (error) => error.code,
            'code',
            'UPLOAD_RESULT_UNCONFIRMED',
          ),
        ),
      );

      final unconfirmed = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(unconfirmed.status, TransferStatus.resultUnconfirmed);
      expect(
        adapter.requests
            .where(
              (request) =>
                  request.method == 'POST' &&
                  request.path == '/api/v1/upload-sessions',
            )
            .length,
        1,
      );

      await harness.controller.removeTransfer(unconfirmed.id);
      expect(
        harness.container.read(clientControllerProvider).transfers,
        isEmpty,
      );
      final payloadDirectory = Directory(
        '${transferDirectory.path}${Platform.pathSeparator}payloads',
      );
      expect(
        payloadDirectory.listSync().whereType<File>().where(
          (file) => file.path.endsWith('.upload'),
        ),
        isEmpty,
      );
    },
  );

  test(
    'an expired known upload session is never recreated implicitly',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter()..disconnectNextUpload = true;
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-upload-expired-session-',
      );
      addTearDown(() => root.delete(recursive: true));
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
        transferDirectory: Directory('${root.path}/store'),
      );
      addTearDown(harness.container.dispose);
      final source = File('${root.path}/payload.bin');
      await source.writeAsBytes(<int>[1, 2, 3], flush: true);
      await harness.connectAndLogin(endpoint, username: 'owner');

      await expectLater(
        harness.controller.uploadFile(
          sourcePath: source.path,
          fileName: 'payload.bin',
          targetDirectory: '/',
        ),
        throwsA(isA<ApiException>()),
      );
      final interrupted = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      adapter.expireUploadSessionStatus = true;

      await expectLater(
        harness.controller.resumeTransfer(interrupted.id),
        throwsA(
          isA<ApiException>().having(
            (error) => error.code,
            'code',
            'UPLOAD_RESULT_UNCONFIRMED',
          ),
        ),
      );

      expect(
        harness.container
            .read(clientControllerProvider)
            .transfers
            .single
            .status,
        TransferStatus.resultUnconfirmed,
      );
      expect(
        adapter.requests
            .where(
              (request) =>
                  request.method == 'POST' &&
                  request.path == '/api/v1/upload-sessions',
            )
            .length,
        1,
      );
    },
  );

  final goneSessionScenarios =
      <({String name, void Function(_ControllerAdapter adapter) configure})>[
        (
          name: 'chunk 410',
          configure: (adapter) {
            adapter.goneNextUploadChunkStatus = 410;
          },
        ),
        (
          name: 'commit 404',
          configure: (adapter) {
            adapter.goneNextUploadCommitStatus = 404;
          },
        ),
        (
          name: 'commit conflict follow-up 410',
          configure: (adapter) {
            adapter.conflictNextUploadCommit = true;
            adapter.expireUploadSessionStatus = true;
          },
        ),
      ];
  for (final scenario in goneSessionScenarios) {
    test(
      'a known upload session ${scenario.name} becomes result unconfirmed',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        scenario.configure(adapter);
        final root = await Directory.systemTemp.createTemp(
          'mnemonas-controller-upload-gone-stage-',
        );
        addTearDown(() => root.delete(recursive: true));
        final transferDirectory = Directory('${root.path}/store');
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
          transferDirectory: transferDirectory,
        );
        addTearDown(harness.container.dispose);
        final source = File('${root.path}/payload.bin');
        await source.writeAsBytes(<int>[1, 2, 3], flush: true);
        await harness.connectAndLogin(endpoint, username: 'owner');

        await expectLater(
          harness.controller.uploadFile(
            sourcePath: source.path,
            fileName: 'payload.bin',
            targetDirectory: '/',
          ),
          throwsA(
            isA<ApiException>().having(
              (error) => error.code,
              'code',
              'UPLOAD_RESULT_UNCONFIRMED',
            ),
          ),
        );

        final transfer = harness.container
            .read(clientControllerProvider)
            .transfers
            .single;
        expect(transfer.status, TransferStatus.resultUnconfirmed);
        expect(transfer.errorMessage, contains('无法确认'));
        expect(
          adapter.requests
              .where(
                (request) =>
                    request.method == 'POST' &&
                    request.path == '/api/v1/upload-sessions',
              )
              .length,
          1,
        );
        final payloadDirectory = Directory(
          '${transferDirectory.path}${Platform.pathSeparator}payloads',
        );
        expect(
          payloadDirectory.listSync().whereType<File>().where(
            (file) => file.path.endsWith('.upload'),
          ),
          hasLength(1),
        );
      },
    );
  }

  for (final goneStatus in <int>[404, 410]) {
    test(
      'removing an upload ignores cancellation status $goneStatus',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter()..disconnectNextUpload = true;
        final root = await Directory.systemTemp.createTemp(
          'mnemonas-controller-upload-gone-cancel-',
        );
        addTearDown(() => root.delete(recursive: true));
        final transferDirectory = Directory('${root.path}/store');
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
          transferDirectory: transferDirectory,
        );
        addTearDown(harness.container.dispose);
        final source = File('${root.path}/payload.bin');
        await source.writeAsBytes(<int>[1, 2, 3], flush: true);
        await harness.connectAndLogin(endpoint, username: 'owner');

        await expectLater(
          harness.controller.uploadFile(
            sourcePath: source.path,
            fileName: 'payload.bin',
            targetDirectory: '/',
          ),
          throwsA(isA<ApiException>()),
        );
        final interrupted = harness.container
            .read(clientControllerProvider)
            .transfers
            .single;
        adapter.goneNextUploadSessionCancelStatus = goneStatus;

        await harness.controller.removeTransfer(interrupted.id);

        expect(
          harness.container.read(clientControllerProvider).transfers,
          isEmpty,
        );
        expect(
          adapter.requests.where(
            (request) =>
                request.method == 'DELETE' &&
                request.path.startsWith('/api/v1/upload-sessions/'),
          ),
          hasLength(1),
        );
        final payloadDirectory = Directory(
          '${transferDirectory.path}${Platform.pathSeparator}payloads',
        );
        expect(
          payloadDirectory.listSync().whereType<File>().where(
            (file) => file.path.endsWith('.upload'),
          ),
          isEmpty,
        );
      },
    );
  }

  test('upload commit conflict never overwrites the target state', () async {
    final endpoint = ServerEndpoint.parse('https://nas.example.com');
    final adapter = _ControllerAdapter()..conflictNextUploadCommit = true;
    final harness = await _ControllerHarness.start(
      adapters: {endpoint.baseUrl: adapter},
    );
    addTearDown(harness.container.dispose);
    final directory = await Directory.systemTemp.createTemp(
      'mnemonas-controller-upload-conflict-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final source = File('${directory.path}/payload.bin');
    await source.writeAsBytes(<int>[1, 2, 3], flush: true);
    await harness.connectAndLogin(endpoint, username: 'owner');

    await expectLater(
      harness.controller.uploadFile(
        sourcePath: source.path,
        fileName: 'payload.bin',
        targetDirectory: '/',
      ),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'UPLOAD_TARGET_CONFLICT',
        ),
      ),
    );

    final transfer = harness.container
        .read(clientControllerProvider)
        .transfers
        .single;
    expect(transfer.status, TransferStatus.failed);
    expect(transfer.canRetry, isFalse);
    expect(transfer.errorMessage, contains('目标文件已发生变化'));
    expect(adapter.uploadSessions.single.state, 'conflict');
  });

  test('startup removes unreferenced private upload payloads', () async {
    final endpoint = ServerEndpoint.parse('https://nas.example.com');
    final root = await Directory.systemTemp.createTemp(
      'mnemonas-controller-upload-orphans-',
    );
    addTearDown(() => root.delete(recursive: true));
    final transferDirectory = Directory('${root.path}/store');
    final payloadDirectory = Directory('${transferDirectory.path}/payloads');
    await payloadDirectory.create(recursive: true);
    final ready = File('${payloadDirectory.path}/orphan.upload');
    final partial = File('${payloadDirectory.path}/orphan.upload.part');
    await ready.writeAsBytes(<int>[1, 2, 3], flush: true);
    await partial.writeAsBytes(<int>[4, 5, 6], flush: true);
    final harness = await _ControllerHarness.start(
      adapters: {endpoint.baseUrl: _ControllerAdapter()},
      transferDirectory: transferDirectory,
    );
    addTearDown(harness.container.dispose);

    await harness.connectAndLogin(endpoint, username: 'owner');

    expect(await ready.exists(), isFalse);
    expect(await partial.exists(), isFalse);
  });

  test(
    'startup retries confirmed upload cleanup but preserves uncertain payloads',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-upload-terminal-cleanup-',
      );
      addTearDown(() => root.delete(recursive: true));
      final transferDirectory = Directory('${root.path}/store');
      final payloadDirectory = Directory('${transferDirectory.path}/payloads');
      await payloadDirectory.create(recursive: true);
      final completedPath = '${payloadDirectory.path}/completed.upload';
      final uncertainPath = '${payloadDirectory.path}/uncertain.upload';
      await File(completedPath).writeAsBytes(<int>[1, 2, 3], flush: true);
      await File(uncertainPath).writeAsBytes(<int>[4, 5, 6], flush: true);
      final now = DateTime.now().toUtc().subtract(const Duration(hours: 1));
      final expiresAt = now.add(const Duration(hours: 72));
      final store = FileTransferStore(directoryPath: transferDirectory.path);
      await store.upsert(
        transfer_core.TransferTask(
          id: 'upload-completed',
          direction: transfer_core.TransferDirection.upload,
          phase: transfer_core.TransferPhase.completed,
          endpointBaseUrl: endpoint.baseUrl,
          userId: 'user-owner',
          remotePath: '/completed.bin',
          displayName: 'completed.bin',
          stagingPath: completedPath,
          payloadSha256: '0' * 64,
          uploadSessionId: 'session-completed',
          uploadSessionExpiresAt: expiresAt,
          durableOffset: 3,
          totalBytes: 3,
          createdAt: now,
          updatedAt: now,
        ),
      );
      await store.upsert(
        transfer_core.TransferTask(
          id: 'upload-uncertain',
          direction: transfer_core.TransferDirection.upload,
          phase: transfer_core.TransferPhase.resultUnconfirmed,
          endpointBaseUrl: endpoint.baseUrl,
          userId: 'user-owner',
          remotePath: '/uncertain.bin',
          displayName: 'uncertain.bin',
          stagingPath: uncertainPath,
          payloadSha256: '1' * 64,
          uploadSessionId: 'session-uncertain',
          uploadSessionExpiresAt: expiresAt,
          durableOffset: 3,
          totalBytes: 3,
          createdAt: now,
          updatedAt: now,
          errorCode: 'UPLOAD_RESULT_UNCONFIRMED',
          errorMessage: 'The upload result cannot be confirmed',
        ),
      );
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: _ControllerAdapter()},
        transferDirectory: transferDirectory,
      );
      addTearDown(harness.container.dispose);

      await harness.connectAndLogin(endpoint, username: 'owner');

      expect(await File(completedPath).exists(), isFalse);
      expect(await File(uncertainPath).exists(), isTrue);
      expect(
        harness.container.read(clientControllerProvider).transfers,
        hasLength(2),
      );
    },
  );

  test(
    'interrupted download resumes from durable state after restart',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-download-resume-',
      );
      addTearDown(() => root.delete(recursive: true));
      final transferDirectory = Directory('${root.path}/store');
      final destination = File('${root.path}/payload.bin');
      final firstAdapter = _ControllerAdapter()..interruptNextDownload = true;
      final first = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: firstAdapter},
        transferDirectory: transferDirectory,
      );
      addTearDown(first.container.dispose);
      await first.connectAndLogin(endpoint, username: 'owner');
      final entry = FileEntry(
        name: 'payload.bin',
        path: '/payload.bin',
        isDirectory: false,
        size: 6,
        modifiedAt: DateTime.utc(2026, 7, 19, 12),
        capabilities: const FileCapabilities(
          read: true,
          concreteRead: true,
          write: true,
        ),
      );

      await expectLater(
        first.controller.downloadFile(
          entry: entry,
          destinationPath: destination.path,
          overwrite: true,
        ),
        throwsA(
          isA<ApiException>().having(
            (error) => error.kind,
            'kind',
            ApiFailureKind.connection,
          ),
        ),
      );
      final interrupted = first.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(interrupted.status, TransferStatus.paused);
      expect(interrupted.transferred, 3);

      final secondAdapter = _ControllerAdapter();
      final second = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: secondAdapter},
        transferDirectory: transferDirectory,
      );
      addTearDown(second.container.dispose);
      await second.connectAndLogin(endpoint, username: 'owner');
      final restored = second.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(restored.id, interrupted.id);
      expect(restored.status, TransferStatus.paused);
      expect(restored.transferred, 3);

      await Future.wait<void>(<Future<void>>[
        second.controller.resumeTransfer(restored.id),
        second.controller.resumeTransfer(restored.id),
      ]);

      expect(await destination.readAsBytes(), <int>[1, 2, 3, 4, 5, 6]);
      expect(secondAdapter.downloadRanges, <String?>['bytes=3-']);
      expect(
        second.container.read(clientControllerProvider).transfers.single.status,
        TransferStatus.completed,
      );
    },
  );

  test(
    'startup reconciles a running task with the actual partial length',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-running-recovery-',
      );
      addTearDown(() => root.delete(recursive: true));
      final transferDirectory = Directory('${root.path}/store');
      final payloadDirectory = Directory('${transferDirectory.path}/payloads');
      await payloadDirectory.create(recursive: true);
      final payloadPath = '${payloadDirectory.path}/task-running.payload';
      await File('$payloadPath.part').writeAsBytes(<int>[1, 2, 3], flush: true);
      final now = DateTime.now().toUtc().subtract(const Duration(hours: 1));
      await FileTransferStore(directoryPath: transferDirectory.path).upsert(
        transfer_core.TransferTask(
          id: 'task-running',
          direction: transfer_core.TransferDirection.download,
          phase: transfer_core.TransferPhase.running,
          endpointBaseUrl: endpoint.baseUrl,
          userId: 'user-owner',
          remotePath: '/payload.bin',
          displayName: 'payload.bin',
          stagingPath: payloadPath,
          destinationPath: '${root.path}/payload.bin',
          validator: 'download-identity-1',
          durableOffset: 1,
          totalBytes: 6,
          createdAt: now,
          updatedAt: now,
        ),
      );
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: _ControllerAdapter()},
        transferDirectory: transferDirectory,
      );
      addTearDown(harness.container.dispose);

      await harness.connectAndLogin(endpoint, username: 'owner');

      final restored = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(restored.id, 'task-running');
      expect(restored.status, TransferStatus.paused);
      expect(restored.transferred, 3);
      expect(restored.errorMessage, contains('客户端重启'));
    },
  );

  test(
    'startup commits a complete partial without requesting an invalid range',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-complete-partial-',
      );
      addTearDown(() => root.delete(recursive: true));
      final transferDirectory = Directory('${root.path}/store');
      final payloadDirectory = Directory('${transferDirectory.path}/payloads');
      await payloadDirectory.create(recursive: true);
      final payloadPath = '${payloadDirectory.path}/task-complete.payload';
      await File(
        '$payloadPath.part',
      ).writeAsBytes(<int>[1, 2, 3, 4, 5, 6], flush: true);
      final destination = File('${root.path}/payload.bin');
      final now = DateTime.now().toUtc().subtract(const Duration(hours: 1));
      await FileTransferStore(directoryPath: transferDirectory.path).upsert(
        transfer_core.TransferTask(
          id: 'task-complete',
          direction: transfer_core.TransferDirection.download,
          phase: transfer_core.TransferPhase.running,
          endpointBaseUrl: endpoint.baseUrl,
          userId: 'user-owner',
          remotePath: '/payload.bin',
          displayName: 'payload.bin',
          stagingPath: payloadPath,
          destinationPath: destination.path,
          validator: 'download-identity-1',
          durableOffset: 6,
          totalBytes: 6,
          createdAt: now,
          updatedAt: now,
        ),
      );
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
        transferDirectory: transferDirectory,
      );
      addTearDown(harness.container.dispose);

      await harness.connectAndLogin(endpoint, username: 'owner');

      final restored = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(restored.id, 'task-complete');
      expect(restored.status, TransferStatus.paused);
      expect(restored.transferred, 6);

      await harness.controller.resumeTransfer(restored.id);

      expect(adapter.downloadRanges, isEmpty);
      expect(await destination.readAsBytes(), <int>[1, 2, 3, 4, 5, 6]);
      expect(
        harness.container
            .read(clientControllerProvider)
            .transfers
            .single
            .status,
        TransferStatus.completed,
      );
      expect(File('$payloadPath.part').existsSync(), isFalse);
    },
  );

  test('startup clears a temporary Android destination URI', () async {
    final endpoint = ServerEndpoint.parse('https://nas.example.com');
    final root = await Directory.systemTemp.createTemp(
      'mnemonas-controller-stale-destination-',
    );
    addTearDown(() => root.delete(recursive: true));
    final transferDirectory = Directory('${root.path}/store');
    final payloadDirectory = Directory('${transferDirectory.path}/payloads');
    await payloadDirectory.create(recursive: true);
    final payloadPath = '${payloadDirectory.path}/task-destination.payload';
    await File(payloadPath).writeAsBytes(<int>[1, 2, 3, 4, 5, 6], flush: true);
    final now = DateTime.now().toUtc().subtract(const Duration(hours: 1));
    final store = FileTransferStore(directoryPath: transferDirectory.path);
    await store.upsert(
      transfer_core.TransferTask(
        id: 'task-destination',
        direction: transfer_core.TransferDirection.download,
        phase: transfer_core.TransferPhase.awaitingDestination,
        endpointBaseUrl: endpoint.baseUrl,
        userId: 'user-owner',
        remotePath: '/payload.bin',
        displayName: 'payload.bin',
        stagingPath: payloadPath,
        destinationUri: 'content://downloads/document/stale',
        validator: 'download-identity-1',
        durableOffset: 6,
        totalBytes: 6,
        createdAt: now,
        updatedAt: now,
      ),
    );
    final harness = await _ControllerHarness.start(
      adapters: {endpoint.baseUrl: _ControllerAdapter()},
      transferDirectory: transferDirectory,
    );
    addTearDown(harness.container.dispose);

    await harness.connectAndLogin(endpoint, username: 'owner');

    final restored = harness.container
        .read(clientControllerProvider)
        .transfers
        .single;
    expect(restored.status, TransferStatus.awaitingDestination);
    expect(restored.errorMessage, contains('重新选择保存位置'));
    final stored = (await store.load()).tasks.single;
    expect(stored.destinationUri, isNull);
  });

  test(
    'startup recovers a fully staged Android download before destination choice',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-staged-recovery-',
      );
      addTearDown(() => root.delete(recursive: true));
      final transferDirectory = Directory('${root.path}/store');
      final payloadDirectory = Directory('${transferDirectory.path}/payloads');
      await payloadDirectory.create(recursive: true);
      final payloadPath = '${payloadDirectory.path}/task-staged.payload';
      await File(
        payloadPath,
      ).writeAsBytes(<int>[1, 2, 3, 4, 5, 6], flush: true);
      final now = DateTime.now().toUtc().subtract(const Duration(hours: 1));
      final store = FileTransferStore(directoryPath: transferDirectory.path);
      await store.upsert(
        transfer_core.TransferTask(
          id: 'task-staged',
          direction: transfer_core.TransferDirection.download,
          phase: transfer_core.TransferPhase.running,
          endpointBaseUrl: endpoint.baseUrl,
          userId: 'user-owner',
          remotePath: '/payload.bin',
          displayName: 'payload.bin',
          stagingPath: payloadPath,
          validator: 'download-identity-1',
          durableOffset: 6,
          totalBytes: 6,
          createdAt: now,
          updatedAt: now,
        ),
      );
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: _ControllerAdapter()},
        transferDirectory: transferDirectory,
      );
      addTearDown(harness.container.dispose);

      await harness.connectAndLogin(endpoint, username: 'owner');

      final restored = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(restored.status, TransferStatus.awaitingDestination);
      expect(restored.errorMessage, isNull);
      final stored = (await store.load()).tasks.single;
      expect(stored.phase, transfer_core.TransferPhase.awaitingDestination);
      expect(stored.destinationUri, isNull);
      expect(File(payloadPath).existsSync(), isTrue);
    },
  );

  test(
    'Android destination failure remains durable and can be retried',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-destination-retry-',
      );
      addTearDown(() => root.delete(recursive: true));
      var failMaterialization = true;
      final materializedSources = <String>[];
      final materializedUris = <String>[];
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: _ControllerAdapter()},
        transferDirectory: Directory('${root.path}/store'),
        contentUriMaterializer:
            ({
              required sourcePath,
              required destinationUri,
              required operationId,
              required onProgress,
            }) async {
              expect(operationId, isNotEmpty);
              materializedSources.add(sourcePath);
              materializedUris.add(destinationUri);
              onProgress(3, 6);
              if (failMaterialization) {
                throw StateError('destination unavailable');
              }
            },
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      final entry = FileEntry(
        name: 'payload.bin',
        path: '/payload.bin',
        isDirectory: false,
        size: 6,
        modifiedAt: DateTime.utc(2026, 7, 19, 12),
        capabilities: const FileCapabilities(
          read: true,
          concreteRead: true,
          write: true,
        ),
      );

      await expectLater(
        harness.controller.downloadFile(
          entry: entry,
          destinationUri: 'content://downloads/document/42',
        ),
        throwsStateError,
      );
      final pending = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(pending.status, TransferStatus.awaitingDestination);
      expect(File(materializedSources.single).existsSync(), isTrue);

      failMaterialization = false;
      await harness.controller.resumeTransfer(pending.id);

      expect(materializedUris, <String>[
        'content://downloads/document/42',
        'content://downloads/document/42',
      ]);
      expect(File(materializedSources.last).existsSync(), isFalse);
      expect(
        harness.container
            .read(clientControllerProvider)
            .transfers
            .single
            .status,
        TransferStatus.completed,
      );
    },
  );

  test(
    'Android download selects its document destination after network staging',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-deferred-destination-',
      );
      addTearDown(() => root.delete(recursive: true));
      final materializedSources = <String>[];
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: _ControllerAdapter()},
        transferDirectory: Directory('${root.path}/store'),
        contentUriMaterializer:
            ({
              required sourcePath,
              required destinationUri,
              required operationId,
              required onProgress,
            }) async {
              expect(destinationUri, 'content://downloads/document/deferred');
              expect(operationId, isNotEmpty);
              onProgress(6, 6);
              materializedSources.add(sourcePath);
            },
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      final entry = FileEntry(
        name: 'payload.bin',
        path: '/payload.bin',
        isDirectory: false,
        size: 6,
        modifiedAt: DateTime.utc(2026, 7, 19, 12),
        capabilities: const FileCapabilities(
          read: true,
          concreteRead: true,
          write: true,
        ),
      );

      final id = await harness.controller.stageDownloadForDestination(
        entry: entry,
      );

      final staged = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(staged.id, id);
      expect(staged.status, TransferStatus.awaitingDestination);
      expect(materializedSources, isEmpty);

      await harness.controller.setDownloadDestination(
        id: id,
        destinationUri: 'content://downloads/document/deferred',
      );

      expect(materializedSources, hasLength(1));
      expect(File(materializedSources.single).existsSync(), isFalse);
      expect(
        harness.container
            .read(clientControllerProvider)
            .transfers
            .single
            .status,
        TransferStatus.completed,
      );
    },
  );

  test(
    'Android destination copy is cancelled through the durable task',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-destination-cancel-',
      );
      addTearDown(() => root.delete(recursive: true));
      final copyStarted = Completer<void>();
      final copyResult = Completer<void>();
      final cancelledOperations = <String>[];
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: _ControllerAdapter()},
        transferDirectory: Directory('${root.path}/store'),
        contentUriMaterializer:
            ({
              required sourcePath,
              required destinationUri,
              required operationId,
              required onProgress,
            }) async {
              onProgress(1, 6);
              copyStarted.complete();
              await copyResult.future;
            },
        contentUriMaterializationCanceller: (operationId) async {
          cancelledOperations.add(operationId);
          if (!copyResult.isCompleted) {
            copyResult.completeError(
              const ApiException(
                kind: ApiFailureKind.cancelled,
                code: 'EXPORT_CANCELLED',
                message: 'The export was cancelled',
              ),
            );
          }
        },
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      final entry = FileEntry(
        name: 'payload.bin',
        path: '/payload.bin',
        isDirectory: false,
        size: 6,
        modifiedAt: DateTime.utc(2026, 7, 19, 12),
        capabilities: const FileCapabilities(
          read: true,
          concreteRead: true,
          write: true,
        ),
      );

      final download = harness.controller.downloadFile(
        entry: entry,
        destinationUri: 'content://downloads/document/cancelled',
      );
      await copyStarted.future;
      final transfer = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      harness.controller.pauseTransfer(transfer.id);

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
      expect(cancelledOperations, <String>[transfer.id]);
      final pending = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(pending.status, TransferStatus.awaitingDestination);
      expect(pending.errorMessage, contains('重新选择位置'));
    },
  );

  test(
    'app background fences a download while durable task creation is waiting',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-download-start-fence-',
      );
      addTearDown(() => root.delete(recursive: true));
      final transferDirectory = Directory('${root.path}/store');
      final startGate = _RequestGate();
      addTearDown(startGate.release);
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
        transferDirectory: transferDirectory,
        transferStartBarrier: startGate.wait,
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      final observedStatuses = <TransferStatus>[];
      final subscription = harness.container.listen<ClientState>(
        clientControllerProvider,
        (_, next) {
          observedStatuses.addAll(
            next.transfers.map((transfer) => transfer.status),
          );
        },
      );
      addTearDown(subscription.close);

      final download = harness.controller.downloadFile(
        entry: FileEntry(
          name: 'start-fenced.bin',
          path: '/start-fenced.bin',
          isDirectory: false,
          size: 6,
          modifiedAt: DateTime.utc(2026, 7, 19, 12),
          capabilities: const FileCapabilities(
            read: true,
            concreteRead: true,
            write: true,
          ),
        ),
        destinationPath: '${root.path}/start-fenced.bin',
      );
      final downloadFinished = expectLater(
        download,
        throwsA(
          isA<ApiException>().having(
            (error) => error.code,
            'code',
            'APP_BACKGROUNDED',
          ),
        ),
      );

      await startGate.started.timeout(const Duration(seconds: 2));
      expect(
        await harness.controller.pauseActiveTransfersForAppBackground(),
        0,
      );
      expect(adapter.downloadRanges, isEmpty);
      startGate.release();
      await downloadFinished;

      expect(adapter.downloadRanges, isEmpty);
      expect(observedStatuses, isNot(contains(TransferStatus.running)));
      final transfer = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(transfer.status, TransferStatus.paused);
      expect(transfer.canRetry, isTrue);
      expect(transfer.errorMessage, contains('后台'));
      final stored = (await FileTransferStore(
        directoryPath: transferDirectory.path,
      ).load()).tasks.single;
      expect(stored.phase, transfer_core.TransferPhase.paused);
      expect(stored.errorCode, 'APP_BACKGROUNDED');
      expect(stored.errorMessage, contains('后台'));
      expect(File('${root.path}/start-fenced.bin').existsSync(), isFalse);
    },
  );

  test(
    'app background fences an upload while durable task creation is waiting',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-upload-start-fence-',
      );
      addTearDown(() => root.delete(recursive: true));
      final transferDirectory = Directory('${root.path}/store');
      final startGate = _RequestGate();
      addTearDown(startGate.release);
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
        transferDirectory: transferDirectory,
        transferStartBarrier: startGate.wait,
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      final source = File('${root.path}/upload-source.bin');
      await source.writeAsBytes(<int>[1, 2, 3], flush: true);
      final observedStatuses = <TransferStatus>[];
      final subscription = harness.container.listen<ClientState>(
        clientControllerProvider,
        (_, next) {
          observedStatuses.addAll(
            next.transfers.map((transfer) => transfer.status),
          );
        },
      );
      addTearDown(subscription.close);

      final upload = harness.controller.uploadFile(
        sourcePath: source.path,
        fileName: 'start-fenced.bin',
        targetDirectory: '/',
      );
      final uploadFinished = expectLater(
        upload,
        throwsA(
          isA<ApiException>().having(
            (error) => error.code,
            'code',
            'APP_BACKGROUNDED',
          ),
        ),
      );

      await startGate.started.timeout(const Duration(seconds: 2));
      expect(
        await harness.controller.pauseActiveTransfersForAppBackground(),
        1,
      );
      expect(
        adapter.requests.where(
          (request) => request.path.startsWith('/api/v1/upload-sessions'),
        ),
        isEmpty,
      );
      startGate.release();
      await uploadFinished;

      expect(
        adapter.requests.where(
          (request) => request.path.startsWith('/api/v1/upload-sessions'),
        ),
        isEmpty,
      );
      expect(observedStatuses, isNot(contains(TransferStatus.running)));
      final transfer = harness.container
          .read(clientControllerProvider)
          .transfers
          .single;
      expect(transfer.status, TransferStatus.failed);
      expect(transfer.canRetry, isFalse);
      expect(transfer.errorMessage, contains('后台'));
      expect(
        (await FileTransferStore(
          directoryPath: transferDirectory.path,
        ).load()).tasks,
        isEmpty,
      );
    },
  );

  test(
    'app background pauses active Android copies once and preserves idle tasks',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-background-pause-',
      );
      addTearDown(() => root.delete(recursive: true));
      final transferDirectory = Directory('${root.path}/store');
      final copies = _ContentUriCopyGate(expectedOperationCount: 2);
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: _ControllerAdapter()},
        transferDirectory: transferDirectory,
        contentUriMaterializer: copies.materialize,
        contentUriMaterializationCanceller: copies.cancel,
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');

      FileEntry entry(String name) => FileEntry(
        name: name,
        path: '/$name',
        isDirectory: false,
        size: 6,
        modifiedAt: DateTime.utc(2026, 7, 19, 12),
        capabilities: const FileCapabilities(
          read: true,
          concreteRead: true,
          write: true,
        ),
      );

      await harness.controller.downloadFile(
        entry: entry('completed.bin'),
        destinationPath: '${root.path}/completed.bin',
      );
      final store = FileTransferStore(directoryPath: transferDirectory.path);
      final completedBefore = (await store.load()).tasks.singleWhere(
        (task) => task.displayName == 'completed.bin',
      );
      final completedBeforeJson = completedBefore.toJson();

      final firstDownload = harness.controller.downloadFile(
        entry: entry('first.bin'),
        destinationUri: 'content://downloads/document/first',
      );
      final secondDownload = harness.controller.downloadFile(
        entry: entry('second.bin'),
        destinationUri: 'content://downloads/document/second',
      );
      final downloadsFinished = expectLater(
        Future.wait<File>(<Future<File>>[firstDownload, secondDownload]),
        throwsA(
          isA<ApiException>().having(
            (error) => error.kind,
            'kind',
            ApiFailureKind.cancelled,
          ),
        ),
      );

      await copies.allStarted.timeout(const Duration(seconds: 2));
      final activeIds = copies.operationIds;
      expect(activeIds, hasLength(2));

      final firstPause = harness.controller
          .pauseActiveTransfersForAppBackground();
      final repeatedPause = harness.controller
          .pauseActiveTransfersForAppBackground();

      expect(await firstPause, 2);
      expect(await repeatedPause, 0);
      await downloadsFinished;
      expect(copies.cancelledOperationIds, unorderedEquals(activeIds));

      final state = harness.container.read(clientControllerProvider);
      final paused = state.transfers.where(
        (transfer) => activeIds.contains(transfer.id),
      );
      expect(paused, hasLength(2));
      for (final transfer in paused) {
        expect(transfer.status, TransferStatus.awaitingDestination);
        expect(transfer.errorMessage, contains('后台'));
      }

      final snapshot = await store.load();
      for (final id in activeIds) {
        final task = snapshot.tasks.singleWhere((task) => task.id == id);
        expect(task.phase, transfer_core.TransferPhase.awaitingDestination);
        expect(task.errorCode, 'APP_BACKGROUNDED');
        expect(task.errorMessage, contains('后台'));
        expect(task.destinationUri, isNull);
      }

      final completedAfter = snapshot.tasks.singleWhere(
        (task) => task.id == completedBefore.id,
      );
      expect(completedAfter.toJson(), equals(completedBeforeJson));
      expect(
        await harness.controller.pauseActiveTransfersForAppBackground(),
        0,
      );
      expect((await store.load()).generation, snapshot.generation);
    },
  );

  test(
    'app background cannot downgrade a durably completed active run',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-terminal-pause-race-',
      );
      addTearDown(() => root.delete(recursive: true));
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: _ControllerAdapter()},
        transferDirectory: Directory('${root.path}/store'),
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      final pauseResult = Completer<int>();
      final subscription = harness.container.listen<ClientState>(
        clientControllerProvider,
        (_, next) {
          if (!pauseResult.isCompleted &&
              next.transfers.any(
                (transfer) => transfer.status == TransferStatus.completed,
              )) {
            unawaited(
              harness.controller.pauseActiveTransfersForAppBackground().then(
                pauseResult.complete,
              ),
            );
          }
        },
      );
      addTearDown(subscription.close);
      final entry = FileEntry(
        name: 'completed-race.bin',
        path: '/completed-race.bin',
        isDirectory: false,
        size: 6,
        modifiedAt: DateTime.utc(2026, 7, 19, 12),
        capabilities: const FileCapabilities(
          read: true,
          concreteRead: true,
          write: true,
        ),
      );

      await harness.controller.downloadFile(
        entry: entry,
        destinationPath: '${root.path}/completed-race.bin',
      );

      expect(await pauseResult.future.timeout(const Duration(seconds: 2)), 0);
      expect(
        harness.container
            .read(clientControllerProvider)
            .transfers
            .single
            .status,
        TransferStatus.completed,
      );
      final snapshot = await FileTransferStore(
        directoryPath: '${root.path}/store',
      ).load();
      expect(
        snapshot.tasks.single.phase,
        transfer_core.TransferPhase.completed,
      );
    },
  );

  test(
    'logout fences completion of an overlapping app-background pause',
    () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-controller-background-logout-',
      );
      addTearDown(() => root.delete(recursive: true));
      final transferDirectory = Directory('${root.path}/store');
      final copies = _ContentUriCopyGate(expectedOperationCount: 1);
      final adapter = _ControllerAdapter();
      final logoutGate = adapter.holdLogout();
      addTearDown(logoutGate.release);
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
        transferDirectory: transferDirectory,
        contentUriMaterializer: copies.materialize,
        contentUriMaterializationCanceller: copies.cancel,
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');

      final download = harness.controller.downloadFile(
        entry: FileEntry(
          name: 'payload.bin',
          path: '/payload.bin',
          isDirectory: false,
          size: 6,
          modifiedAt: DateTime.utc(2026, 7, 19, 12),
          capabilities: const FileCapabilities(
            read: true,
            concreteRead: true,
            write: true,
          ),
        ),
        destinationUri: 'content://downloads/document/logout-race',
      );
      final downloadFinished = expectLater(
        download,
        throwsA(
          isA<ApiException>().having(
            (error) => error.kind,
            'kind',
            ApiFailureKind.cancelled,
          ),
        ),
      );

      await copies.allStarted.timeout(const Duration(seconds: 2));
      final transferId = copies.operationIds.single;
      final pause = harness.controller.pauseActiveTransfersForAppBackground();
      final logout = harness.controller.logout();
      final stalePause = harness.controller
          .pauseActiveTransfersForAppBackground();

      await logoutGate.started.timeout(const Duration(seconds: 2));
      expect(await pause, 1);
      expect(await stalePause, 0);
      await downloadFinished;

      var state = harness.container.read(clientControllerProvider);
      expect(state.stage, ClientStage.needsLogin);
      expect(state.user, isNull);
      expect(state.transfers, isEmpty);

      final stored = (await FileTransferStore(
        directoryPath: transferDirectory.path,
      ).load()).tasks.singleWhere((task) => task.id == transferId);
      expect(stored.phase, transfer_core.TransferPhase.awaitingDestination);
      expect(stored.errorCode, 'APP_BACKGROUNDED');
      expect(stored.destinationUri, isNull);
      expect(copies.cancelledOperationIds, <String>[transferId]);

      logoutGate.release();
      await logout;
      state = harness.container.read(clientControllerProvider);
      expect(state.stage, ClientStage.needsLogin);
      expect(state.user, isNull);
      expect(state.transfers, isEmpty);
      expect(state.notice, isNull);
    },
  );

  group('search request isolation', () {
    test('reverse completion keeps only the latest search result', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');

      final delayed = adapter.holdSearch('old');
      addTearDown(delayed.release);
      final oldSearch = harness.controller.searchFiles('old');
      await delayed.started.timeout(const Duration(seconds: 2));

      await harness.controller.searchFiles('new');
      delayed.release();
      await oldSearch;

      final state = harness.container.read(clientControllerProvider);
      expect(state.stage, ClientStage.ready);
      expect(state.searchQuery, 'new');
      expect(state.search?.query, 'new');
      expect(state.search?.results.single.path, '/search/new.txt');
      expect(state.isSearchBusy, isFalse);
      expect(state.searchErrorMessage, isNull);
    });

    test(
      'clearing search prevents a delayed response from returning',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'owner');

        final delayed = adapter.holdSearch('pending');
        addTearDown(delayed.release);
        final pending = harness.controller.searchFiles('pending');
        await delayed.started.timeout(const Duration(seconds: 2));
        harness.controller.clearSearch();
        delayed.release();
        await pending;

        final state = harness.container.read(clientControllerProvider);
        expect(state.searchQuery, isEmpty);
        expect(state.search, isNull);
        expect(state.searchErrorMessage, isNull);
        expect(state.isSearchBusy, isFalse);
      },
    );

    test('a recoverable search failure keeps the signed-in state', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter()..failNextSearch = true;
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');

      await expectLater(
        harness.controller.searchFiles('report'),
        throwsA(
          isA<ApiException>().having(
            (error) => error.kind,
            'kind',
            ApiFailureKind.connection,
          ),
        ),
      );

      final state = harness.container.read(clientControllerProvider);
      expect(state.stage, ClientStage.ready);
      expect(state.user?.username, 'owner');
      expect(state.searchQuery, 'report');
      expect(state.search, isNull);
      expect(state.searchErrorMessage, isNotNull);
      expect(state.isSearchBusy, isFalse);
    });

    test('same-query refresh failure preserves the last result', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      await harness.controller.searchFiles('report');
      final previous = harness.container.read(clientControllerProvider).search;
      adapter.failNextSearch = true;

      await expectLater(
        harness.controller.searchFiles('report'),
        throwsA(isA<ApiException>()),
      );

      final state = harness.container.read(clientControllerProvider);
      expect(state.search, same(previous));
      expect(state.search?.query, 'report');
      expect(state.searchErrorMessage, isNotNull);
      expect(state.isSearchBusy, isFalse);
    });

    test('an endpoint change fences a delayed search response', () async {
      final oldEndpoint = ServerEndpoint.parse('https://old.example.com');
      final newEndpoint = ServerEndpoint.parse('https://new.example.com');
      final oldAdapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {
          oldEndpoint.baseUrl: oldAdapter,
          newEndpoint.baseUrl: _ControllerAdapter(),
        },
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(oldEndpoint, username: 'owner');

      final delayed = oldAdapter.holdSearch('legacy');
      addTearDown(delayed.release);
      final legacySearch = harness.controller.searchFiles('legacy');
      await delayed.started.timeout(const Duration(seconds: 2));
      await harness.controller.connect(newEndpoint.baseUrl);
      delayed.release();
      await legacySearch;

      final state = harness.container.read(clientControllerProvider);
      expect(state.stage, ClientStage.needsLogin);
      expect(state.endpoint, newEndpoint);
      expect(state.searchQuery, isEmpty);
      expect(state.search, isNull);
      expect(state.searchErrorMessage, isNull);
    });

    test('invalid queries never reach the search endpoint', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');

      await expectLater(
        harness.controller.searchFiles('   '),
        throwsFormatException,
      );
      await expectLater(
        harness.controller.searchFiles(
          'report',
          limit: maxSearchResultLimit + 1,
        ),
        throwsFormatException,
      );

      expect(adapter.searchRequests, isEmpty);
    });

    test(
      'search target resolution is non-committing until presented',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'owner');
        await harness.controller.loadDirectory('/original');
        final original = harness.container.read(clientControllerProvider);

        final response = await harness.controller.resolveDirectoryForSearch(
          '/target',
        );

        var state = harness.container.read(clientControllerProvider);
        expect(state.currentPath, original.currentPath);
        expect(state.directory, same(original.directory));
        expect(response.data.path, '/target');

        harness.controller.presentDirectoryListing(response.data);
        state = harness.container.read(clientControllerProvider);
        expect(state.currentPath, '/target');
        expect(state.directory, same(response.data));
      },
    );

    test('clearing search fences a delayed target resolution', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      await harness.controller.loadDirectory('/original');
      final original = harness.container.read(clientControllerProvider);

      final delayed = adapter.holdDirectory('/target');
      addTearDown(delayed.release);
      final resolution = harness.controller.resolveDirectoryForSearch(
        '/target',
      );
      await delayed.started.timeout(const Duration(seconds: 2));
      harness.controller.clearSearch();
      delayed.release();

      await expectLater(
        resolution,
        throwsA(
          anyOf(
            isA<StateError>(),
            isA<ApiException>().having(
              (error) => error.kind,
              'kind',
              ApiFailureKind.cancelled,
            ),
          ),
        ),
      );
      final state = harness.container.read(clientControllerProvider);
      expect(state.currentPath, original.currentPath);
      expect(state.directory, same(original.directory));
    });
  });

  group('trash mutation safety', () {
    test('a failed refresh marks the cached trash listing as stale', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      await harness.controller.loadTrash();
      adapter.failNextTrashList = true;

      await expectLater(
        harness.controller.loadTrash(),
        throwsA(
          isA<ApiException>().having(
            (error) => error.kind,
            'kind',
            ApiFailureKind.connection,
          ),
        ),
      );

      final state = harness.container.read(clientControllerProvider);
      expect(state.trash?.count, 3);
      expect(state.trashErrorMessage, isNotNull);
      expect(state.isTrashBusy, isFalse);
    });

    test('guest mutations are rejected locally without a request', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter(userRole: 'guest');
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'guest');
      await harness.controller.loadTrash();

      final item = harness.container
          .read(clientControllerProvider)
          .trash!
          .items
          .first;
      final selection = TrashSelectionSnapshot.fromItems(<TrashItem>[item]);

      await expectLater(
        harness.controller.restoreTrashItem(item),
        throwsA(
          isA<ApiException>()
              .having((error) => error.kind, 'kind', ApiFailureKind.local)
              .having((error) => error.code, 'code', 'READ_ONLY_ACCOUNT'),
        ),
      );
      await expectLater(
        harness.controller.deleteTrashSelection(selection),
        throwsA(
          isA<ApiException>()
              .having((error) => error.kind, 'kind', ApiFailureKind.local)
              .having((error) => error.code, 'code', 'READ_ONLY_ACCOUNT'),
        ),
      );

      expect(adapter.trashMutationRequests, isEmpty);
      expect(harness.container.read(clientControllerProvider).trash?.count, 3);
    });

    test('a confirmed exact selection removes only deleted items', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter()
        ..trashEmptyResult = _trashEmptyResult(
          deleted: const <String>['trash-a'],
          remaining: const <String>['trash-b'],
        );
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      await harness.controller.loadTrash();

      final initialItems = harness.container
          .read(clientControllerProvider)
          .trash!
          .items;
      final selection = TrashSelectionSnapshot.fromItems(initialItems.take(2));
      final outcome = await harness.controller.deleteTrashSelection(selection);

      expect(outcome.deletedIds, const <String>['trash-a']);
      expect(outcome.skippedIds, isEmpty);
      expect(outcome.remainingIds, const <String>['trash-b']);
      expect(outcome.reconciled, isFalse);
      expect(outcome.hasWarnings, isTrue);
      expect(
        harness.container
            .read(clientControllerProvider)
            .trash
            ?.items
            .map((item) => item.id),
        orderedEquals(const <String>['trash-b', 'trash-c']),
      );

      final request = adapter.trashMutationRequests.single;
      expect(request.method, 'POST');
      expect(request.path, '/api/v1/trash/empty');
      expect(request.data, {
        'ids': const <String>['trash-a', 'trash-b'],
      });
    });

    test('skipped items are removed without entering the retry set', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter()
        ..trashEmptyResult = _trashEmptyResult(
          deleted: const <String>[],
          remaining: const <String>['trash-b'],
          skipped: const <String>['trash-a'],
        );
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      await harness.controller.loadTrash();

      final initialItems = harness.container
          .read(clientControllerProvider)
          .trash!
          .items;
      final selection = TrashSelectionSnapshot.fromItems(initialItems.take(2));
      adapter.trashItems.removeWhere((item) => item['id'] == 'trash-a');

      final outcome = await harness.controller.deleteTrashSelection(selection);

      expect(outcome.deletedIds, isEmpty);
      expect(outcome.skippedIds, const <String>['trash-a']);
      expect(outcome.remainingIds, const <String>['trash-b']);
      expect(
        harness.container
            .read(clientControllerProvider)
            .trash
            ?.items
            .map((item) => item.id),
        orderedEquals(const <String>['trash-b', 'trash-c']),
      );
    });

    test(
      'a disconnected deletion remains unconfirmed after trash reconciliation',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'owner');
        await harness.controller.loadTrash();

        final initialItems = harness.container
            .read(clientControllerProvider)
            .trash!
            .items;
        final selection = TrashSelectionSnapshot.fromItems(
          initialItems.take(2),
        );
        final requestStart = adapter.trashRequests.length;
        adapter
          ..disconnectNextTrashMutation = true
          ..disconnectedDeletedIds = const <String>{'trash-a'};

        await expectLater(
          harness.controller.deleteTrashSelection(selection),
          throwsA(
            isA<ApiException>().having(
              (error) => error.kind,
              'kind',
              ApiFailureKind.connection,
            ),
          ),
        );

        expect(
          adapter.trashRequests
              .skip(requestStart)
              .map((request) => '${request.method} ${request.path}'),
          orderedEquals(const <String>[
            'POST /api/v1/trash/empty',
            'GET /api/v1/trash/',
          ]),
        );
        final state = harness.container.read(clientControllerProvider);
        expect(
          state.trash?.items.map((item) => item.id),
          orderedEquals(const <String>['trash-b', 'trash-c']),
        );
        expect(state.trashReconciliationRequired, isTrue);
        expect(state.trashErrorMessage, contains('无法证明'));
        expect(state.isTrashBusy, isFalse);

        await harness.controller.loadTrash();
        expect(
          harness.container
              .read(clientControllerProvider)
              .trashReconciliationRequired,
          isFalse,
        );
      },
    );

    test(
      'a disconnected restore is never inferred from a missing trash ID',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'owner');
        await harness.controller.loadTrash();

        final item = harness.container
            .read(clientControllerProvider)
            .trash!
            .items
            .first;
        adapter.disconnectNextTrashMutation = true;

        await expectLater(
          harness.controller.restoreTrashItem(item),
          throwsA(
            isA<ApiException>().having(
              (error) => error.kind,
              'kind',
              ApiFailureKind.connection,
            ),
          ),
        );

        final state = harness.container.read(clientControllerProvider);
        expect(
          state.trash?.items.map((candidate) => candidate.id),
          orderedEquals(const <String>['trash-b', 'trash-c']),
        );
        expect(state.trashReconciliationRequired, isTrue);
        expect(state.trashErrorMessage, contains('无法证明'));
        expect(state.notice, isNot(contains('已恢复')));
      },
    );

    test(
      'a structured restore 500 is reconciled as potentially committed',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'owner');
        await harness.controller.loadTrash();

        final item = harness.container
            .read(clientControllerProvider)
            .trash!
            .items
            .first;
        adapter.failNextTrashRestoreAfterMutation = true;

        await expectLater(
          harness.controller.restoreTrashItem(item),
          throwsA(
            isA<ApiException>()
                .having((error) => error.statusCode, 'statusCode', 500)
                .having(
                  (error) => error.code,
                  'code',
                  'TRASH_PERSISTENCE_FAILED',
                ),
          ),
        );

        final state = harness.container.read(clientControllerProvider);
        expect(
          state.trash?.items.map((candidate) => candidate.id),
          orderedEquals(const <String>['trash-b', 'trash-c']),
        );
        expect(state.trashReconciliationRequired, isTrue);
        expect(state.trashErrorMessage, contains('无法证明'));
        expect(state.notice, isNot(contains('已恢复')));
      },
    );

    test(
      'a structured empty 500 reconciles a partially committed selection',
      () async {
        final endpoint = ServerEndpoint.parse('https://nas.example.com');
        final adapter = _ControllerAdapter();
        final harness = await _ControllerHarness.start(
          adapters: {endpoint.baseUrl: adapter},
        );
        addTearDown(harness.container.dispose);
        await harness.connectAndLogin(endpoint, username: 'owner');
        await harness.controller.loadTrash();

        final items = harness.container
            .read(clientControllerProvider)
            .trash!
            .items;
        final selection = TrashSelectionSnapshot.fromItems(items.take(2));
        adapter
          ..failNextTrashEmptyAfterMutation = true
          ..failedTrashEmptyDeletedIds = const <String>{'trash-a'};

        await expectLater(
          harness.controller.deleteTrashSelection(selection),
          throwsA(
            isA<ApiException>()
                .having((error) => error.statusCode, 'statusCode', 500)
                .having(
                  (error) => error.code,
                  'code',
                  'TRASH_BATCH_PERSISTENCE_FAILED',
                ),
          ),
        );

        final state = harness.container.read(clientControllerProvider);
        expect(
          state.trash?.items.map((candidate) => candidate.id),
          orderedEquals(const <String>['trash-b', 'trash-c']),
        );
        expect(state.trashReconciliationRequired, isTrue);
        expect(state.trashErrorMessage, contains('无法证明'));

        final mutationCount = adapter.trashMutationRequests.length;
        await expectLater(
          harness.controller.deleteTrashSelection(selection),
          throwsA(
            isA<ApiException>().having(
              (error) => error.code,
              'code',
              'TRASH_RECONCILIATION_REQUIRED',
            ),
          ),
        );
        expect(adapter.trashMutationRequests, hasLength(mutationCount));
      },
    );

    test('a stale trash read cannot overwrite a confirmed restore', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      await harness.controller.loadTrash();

      final item = harness.container
          .read(clientControllerProvider)
          .trash!
          .items
          .first;
      final delayedList = adapter.holdTrashList();
      addTearDown(delayedList.release);
      final staleLoad = harness.controller.loadTrash();
      await delayedList.started.timeout(const Duration(seconds: 2));

      await harness.controller.restoreTrashItem(item);
      delayedList.release();
      await staleLoad;

      final state = harness.container.read(clientControllerProvider);
      expect(
        state.trash?.items.map((candidate) => candidate.id),
        orderedEquals(const <String>['trash-b', 'trash-c']),
      );
      expect(state.notice, '项目已恢复。');
      expect(state.trashReconciliationRequired, isFalse);
    });

    test('failed reconciliation blocks subsequent trash mutations', () async {
      final endpoint = ServerEndpoint.parse('https://nas.example.com');
      final adapter = _ControllerAdapter();
      final harness = await _ControllerHarness.start(
        adapters: {endpoint.baseUrl: adapter},
      );
      addTearDown(harness.container.dispose);
      await harness.connectAndLogin(endpoint, username: 'owner');
      await harness.controller.loadTrash();

      final item = harness.container
          .read(clientControllerProvider)
          .trash!
          .items
          .first;
      final selection = TrashSelectionSnapshot.fromItems(<TrashItem>[item]);
      adapter
        ..disconnectNextTrashMutation = true
        ..failNextTrashList = true;

      await expectLater(
        harness.controller.deleteTrashSelection(selection),
        throwsA(
          isA<ApiException>().having(
            (error) => error.kind,
            'kind',
            ApiFailureKind.connection,
          ),
        ),
      );
      final state = harness.container.read(clientControllerProvider);
      expect(state.trashReconciliationRequired, isTrue);
      expect(state.trashErrorMessage, isNotNull);
      expect(state.isTrashBusy, isFalse);
      final mutationCount = adapter.trashMutationRequests.length;
      final requestCount = adapter.requests.length;

      final reconciliationRequired = isA<ApiException>()
          .having((error) => error.kind, 'kind', ApiFailureKind.local)
          .having(
            (error) => error.code,
            'code',
            'TRASH_RECONCILIATION_REQUIRED',
          );
      await expectLater(
        harness.controller.restoreTrashItem(item),
        throwsA(reconciliationRequired),
      );
      await expectLater(
        harness.controller.deleteTrashSelection(selection),
        throwsA(reconciliationRequired),
      );

      expect(adapter.trashMutationRequests, hasLength(mutationCount));
      expect(adapter.requests, hasLength(requestCount));
    });
  });
}

final class _ReadyController extends ClientController {
  @override
  ClientState build() {
    return ClientState(
      stage: ClientStage.ready,
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      user: const AuthUser(
        id: 'user-1',
        username: 'owner',
        role: 'admin',
        homeDirectory: '/',
        mustChangePassword: false,
      ),
      currentPath: '/photos',
      isBusy: true,
      notice: 'stale notice',
      transfers: const <ClientTransfer>[
        ClientTransfer(
          id: 'transfer-1',
          name: 'photo.jpg',
          direction: TransferDirection.download,
          status: TransferStatus.running,
          transferred: 16,
          total: 32,
        ),
      ],
    );
  }
}

final class _TrackingSessionStore implements AuthSessionStore {
  _TrackingSessionStore(this.session);

  int _revision = 0;
  AuthSession? session;
  int clearCount = 0;

  @override
  Future<AuthSessionSnapshot> snapshot() async {
    return AuthSessionSnapshot(revision: _revision, session: session);
  }

  @override
  Future<bool> commitIfRevision(int expectedRevision, AuthSession value) async {
    if (_revision != expectedRevision) {
      return false;
    }
    _revision++;
    session = value;
    return true;
  }

  @override
  Future<AuthSessionSnapshot?> takeAndClearIfRevision(
    int expectedRevision,
  ) async {
    if (_revision != expectedRevision) {
      return null;
    }
    return _takeAndClear();
  }

  @override
  Future<AuthSessionSnapshot> takeAndClear() async => _takeAndClear();

  AuthSessionSnapshot _takeAndClear() {
    final previous = session;
    _revision++;
    clearCount++;
    session = null;
    return AuthSessionSnapshot(revision: _revision, session: previous);
  }
}

final class _DelayedFirstClearSessionStore implements AuthSessionStore {
  final MemoryAuthSessionStore _delegate = MemoryAuthSessionStore();
  final Completer<void> _firstClearStarted = Completer<void>();
  final Completer<void> _firstClearReleased = Completer<void>();
  var _clearCalls = 0;

  Future<void> get firstClearStarted => _firstClearStarted.future;

  void release() {
    if (!_firstClearReleased.isCompleted) {
      _firstClearReleased.complete();
    }
  }

  @override
  Future<AuthSessionSnapshot> snapshot() => _delegate.snapshot();

  @override
  Future<bool> commitIfRevision(int expectedRevision, AuthSession session) =>
      _delegate.commitIfRevision(expectedRevision, session);

  @override
  Future<AuthSessionSnapshot?> takeAndClearIfRevision(int expectedRevision) =>
      _delegate.takeAndClearIfRevision(expectedRevision);

  @override
  Future<AuthSessionSnapshot> takeAndClear() async {
    _clearCalls++;
    if (_clearCalls == 1) {
      if (!_firstClearStarted.isCompleted) {
        _firstClearStarted.complete();
      }
      await _firstClearReleased.future;
    }
    return _delegate.takeAndClear();
  }
}

final class _GatedCommitSessionStore implements AuthSessionStore {
  final MemoryAuthSessionStore _delegate = MemoryAuthSessionStore();
  _RequestGate? _nextCommitGate;

  _RequestGate holdNextCommit() {
    return _nextCommitGate = _RequestGate();
  }

  @override
  Future<AuthSessionSnapshot> snapshot() => _delegate.snapshot();

  @override
  Future<bool> commitIfRevision(
    int expectedRevision,
    AuthSession session,
  ) async {
    final gate = _nextCommitGate;
    _nextCommitGate = null;
    if (gate != null) {
      await gate.wait();
    }
    return _delegate.commitIfRevision(expectedRevision, session);
  }

  @override
  Future<AuthSessionSnapshot?> takeAndClearIfRevision(int expectedRevision) {
    return _delegate.takeAndClearIfRevision(expectedRevision);
  }

  @override
  Future<AuthSessionSnapshot> takeAndClear() => _delegate.takeAndClear();
}

final class _FailingFenceSessionStore implements AuthSessionStore {
  final MemoryAuthSessionStore _delegate = MemoryAuthSessionStore();
  bool failNextConditionalClear = false;

  @override
  Future<AuthSessionSnapshot> snapshot() => _delegate.snapshot();

  @override
  Future<bool> commitIfRevision(int expectedRevision, AuthSession session) {
    return _delegate.commitIfRevision(expectedRevision, session);
  }

  @override
  Future<AuthSessionSnapshot?> takeAndClearIfRevision(int expectedRevision) {
    if (failNextConditionalClear) {
      failNextConditionalClear = false;
      throw const AuthSessionStoreException('test_clear');
    }
    return _delegate.takeAndClearIfRevision(expectedRevision);
  }

  @override
  Future<AuthSessionSnapshot> takeAndClear() => _delegate.takeAndClear();
}

final class _ControllerHarness {
  const _ControllerHarness({
    required this.container,
    required this.controller,
    required this.sessionStore,
    required this.preferences,
  });

  static Future<_ControllerHarness> start({
    required Map<String, _ControllerAdapter> adapters,
    ServerEndpoint? savedEndpoint,
    bool waitForBootstrap = true,
    ClientController Function()? controllerFactory,
    AuthSessionStore? sessionStore,
    Directory? transferDirectory,
    TransferStartBarrier? transferStartBarrier,
    ContentUriMaterializer? contentUriMaterializer,
    ContentUriMaterializationCanceller? contentUriMaterializationCanceller,
  }) async {
    final preferences = _FakeServerPreferencesStore(savedEndpoint);
    final activeSessionStore = sessionStore ?? MemoryAuthSessionStore();
    final factory = controllerFactory ?? ClientController.new;
    final ownsTransferDirectory = transferDirectory == null;
    final activeTransferDirectory =
        transferDirectory ??
        await Directory.systemTemp.createTemp('mnemonas-controller-transfers-');
    final container = ProviderContainer(
      overrides: [
        serverPreferencesProvider.overrideWithValue(preferences),
        authSessionStoreProvider.overrideWithValue(activeSessionStore),
        transferStoreFactoryProvider.overrideWith((ref) {
          if (ownsTransferDirectory) {
            ref.onDispose(() {
              unawaited(activeTransferDirectory.delete(recursive: true));
            });
          }
          return () async =>
              FileTransferStore(directoryPath: activeTransferDirectory.path);
        }),
        if (transferStartBarrier != null)
          transferStartBarrierProvider.overrideWithValue(transferStartBarrier),
        if (contentUriMaterializer != null)
          contentUriMaterializerProvider.overrideWithValue(
            contentUriMaterializer,
          ),
        if (contentUriMaterializer != null)
          contentUriMaterializationCancellerProvider.overrideWithValue(
            contentUriMaterializationCanceller ?? (_) async {},
          ),
        apiClientFactoryProvider.overrideWithValue((endpoint, store) {
          final adapter = adapters[endpoint.baseUrl];
          if (adapter == null) {
            throw StateError('No adapter configured for ${endpoint.baseUrl}');
          }
          return ApiClient(
            endpoint: endpoint,
            sessionStore: store,
            dio: Dio()..httpClientAdapter = adapter,
          );
        }),
        clientControllerProvider.overrideWith(factory),
      ],
    );
    final controller = container.read(clientControllerProvider.notifier);
    final harness = _ControllerHarness(
      container: container,
      controller: controller,
      sessionStore: activeSessionStore,
      preferences: preferences,
    );
    if (waitForBootstrap) {
      await _waitUntil(
        () =>
            container.read(clientControllerProvider).stage ==
            ClientStage.needsConnection,
      );
    }
    return harness;
  }

  final ProviderContainer container;
  final ClientController controller;
  final AuthSessionStore sessionStore;
  final _FakeServerPreferencesStore preferences;

  Future<void> connectAndLogin(
    ServerEndpoint endpoint, {
    required String username,
  }) async {
    await controller.connect(endpoint.baseUrl);
    expect(
      container.read(clientControllerProvider).stage,
      ClientStage.needsLogin,
    );
    await controller.login(username: username, password: 'password');
    expect(container.read(clientControllerProvider).stage, ClientStage.ready);
  }
}

final class _FakeServerPreferencesStore implements ServerPreferencesStore {
  _FakeServerPreferencesStore(this.endpoint);

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

final class _RecordingController extends ClientController {
  int updateCount = 0;

  @override
  bool updateShouldNotify(ClientState previous, ClientState next) {
    updateCount++;
    return super.updateShouldNotify(previous, next);
  }
}

final class _RequestGate {
  final Completer<void> _started = Completer<void>();
  final Completer<void> _released = Completer<void>();
  final Completer<void> _finished = Completer<void>();

  Future<void> get started => _started.future;

  Future<void> get finished => _finished.future;

  Future<void> wait() async {
    if (!_started.isCompleted) {
      _started.complete();
    }
    await _released.future;
    if (!_finished.isCompleted) {
      _finished.complete();
    }
  }

  void release() {
    if (!_released.isCompleted) {
      _released.complete();
    }
  }
}

final class _ContentUriCopyGate {
  _ContentUriCopyGate({required this.expectedOperationCount});

  final int expectedOperationCount;
  final Completer<void> _allStarted = Completer<void>();
  final Map<String, Completer<void>> _operations = <String, Completer<void>>{};
  final List<String> cancelledOperationIds = <String>[];

  Future<void> get allStarted => _allStarted.future;

  Set<String> get operationIds => _operations.keys.toSet();

  Future<void> materialize({
    required String sourcePath,
    required String destinationUri,
    required String operationId,
    required void Function(int transferred, int? total) onProgress,
  }) async {
    if (_operations.containsKey(operationId)) {
      throw StateError('Duplicate content URI operation: $operationId');
    }
    final result = Completer<void>();
    _operations[operationId] = result;
    onProgress(1, 6);
    if (_operations.length == expectedOperationCount &&
        !_allStarted.isCompleted) {
      _allStarted.complete();
    }
    await result.future;
  }

  Future<void> cancel(String operationId) async {
    cancelledOperationIds.add(operationId);
    final result = _operations[operationId];
    if (result == null) {
      throw StateError('Unknown content URI operation: $operationId');
    }
    if (!result.isCompleted) {
      result.completeError(
        const ApiException(
          kind: ApiFailureKind.cancelled,
          code: 'EXPORT_CANCELLED',
          message: 'The export was cancelled',
        ),
      );
    }
  }
}

final class _ControllerAdapter implements HttpClientAdapter {
  _ControllerAdapter({this.userRole = 'admin'})
    : trashItems = _defaultTrashItems();

  final String userRole;
  final List<Map<String, dynamic>> trashItems;
  final List<_ControllerRequest> requests = <_ControllerRequest>[];
  final Map<String, _RequestGate> _loginGates = {};
  final Map<String, _RequestGate> _directoryGates = {};
  final Map<String, _RequestGate> _searchGates = {};
  _RequestGate? _probeGate;
  _RequestGate? _trashListGate;
  _RequestGate? _trashMutationGate;
  _RequestGate? _passwordChangeGate;
  _RequestGate? _logoutGate;
  _RequestGate? _fileMutationGate;
  _RequestGate? _uploadCreateGate;
  Map<String, dynamic>? trashEmptyResult;
  bool disconnectNextTrashMutation = false;
  bool disconnectNextPasswordChange = false;
  bool rejectNextPasswordChange = false;
  Set<String> disconnectedDeletedIds = const <String>{};
  bool failNextTrashRestoreAfterMutation = false;
  bool failNextTrashEmptyAfterMutation = false;
  Set<String> failedTrashEmptyDeletedIds = const <String>{};
  bool failNextTrashList = false;
  bool failNextSearch = false;
  bool failNextStats = false;
  bool disconnectNextUploadCreate = false;
  bool disconnectNextUpload = false;
  bool expireNextUploadCreate = false;
  int? goneNextUploadLookupStatus;
  bool expireUploadSessionStatus = false;
  int? goneNextUploadSessionCancelStatus;
  int? goneNextUploadChunkStatus;
  int? goneNextUploadCommitStatus;
  bool conflictNextUploadCommit = false;
  bool disconnectNextDirectory = false;
  bool disconnectNextFileMutation = false;
  int? nextFileMutationStatus;
  bool interruptNextDownload = false;
  List<int> downloadPayload = <int>[1, 2, 3, 4, 5, 6];
  final List<String?> downloadRanges = <String?>[];
  final Map<String, _UploadSessionFixture> _uploadSessions =
      <String, _UploadSessionFixture>{};
  int _nextUploadSession = 1;

  List<_UploadSessionFixture> get uploadSessions =>
      _uploadSessions.values.toList(growable: false);

  void forgetUploadSessions() {
    _uploadSessions.clear();
  }

  List<_ControllerRequest> get trashRequests => requests
      .where((request) => request.path.startsWith('/api/v1/trash/'))
      .toList(growable: false);

  List<_ControllerRequest> get trashMutationRequests => trashRequests
      .where((request) => request.method != 'GET')
      .toList(growable: false);

  List<_ControllerRequest> get searchRequests => requests
      .where((request) => request.path == '/api/v1/search')
      .toList(growable: false);

  _RequestGate holdLogin(String username) {
    return _loginGates[username] = _RequestGate();
  }

  _RequestGate holdDirectory(String path) {
    return _directoryGates[path] = _RequestGate();
  }

  _RequestGate holdSearch(String query) {
    return _searchGates[query] = _RequestGate();
  }

  _RequestGate holdProbe() {
    return _probeGate = _RequestGate();
  }

  _RequestGate holdTrashList() {
    return _trashListGate = _RequestGate();
  }

  _RequestGate holdTrashMutation() {
    return _trashMutationGate = _RequestGate();
  }

  _RequestGate holdPasswordChange() {
    return _passwordChangeGate = _RequestGate();
  }

  _RequestGate holdLogout() {
    return _logoutGate = _RequestGate();
  }

  _RequestGate holdFileMutation() {
    return _fileMutationGate = _RequestGate();
  }

  _RequestGate holdUploadCreate() {
    return _uploadCreateGate = _RequestGate();
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
      _ControllerRequest(
        method: options.method,
        path: path,
        query: Map<String, dynamic>.from(options.queryParameters),
        data: options.data,
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
      final gate = _probeGate;
      if (gate != null) {
        await gate.wait();
      }
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
      final body = Map<String, dynamic>.from(options.data! as Map);
      final username = body['username']! as String;
      final gate = _loginGates[username];
      if (gate != null) {
        await gate.wait();
      }
      return _envelope({
        'access_token': 'access-$username',
        'refresh_token': 'refresh-$username',
        'expires_at': '2026-07-20T12:00:00Z',
        'token_type': 'Bearer',
        'user': _user(username),
      });
    }
    if (path == '/api/v1/auth/logout' && options.method == 'POST') {
      final gate = _logoutGate;
      _logoutGate = null;
      if (gate != null) {
        await gate.wait();
      }
      return _envelope(<String, dynamic>{});
    }
    if (path == '/api/v1/auth/me') {
      return _envelope({'user': _user('owner')});
    }
    if (path == '/api/v1/auth/password' && options.method == 'POST') {
      final gate = _passwordChangeGate;
      _passwordChangeGate = null;
      if (gate != null) {
        await gate.wait();
      }
      if (disconnectNextPasswordChange) {
        disconnectNextPasswordChange = false;
        throw DioException(
          requestOptions: options,
          type: DioExceptionType.connectionError,
          error: 'password change connection interrupted',
        );
      }
      if (rejectNextPasswordChange) {
        rejectNextPasswordChange = false;
        return _json(401, {
          'code': 'INVALID_PASSWORD',
          'message': 'current password is incorrect',
        });
      }
      return _envelope(<String, dynamic>{});
    }
    if (path == '/api/v1/stats') {
      if (failNextStats) {
        failNextStats = false;
        throw DioException(
          requestOptions: options,
          type: DioExceptionType.connectionError,
          error: 'statistics connection interrupted',
        );
      }
      return _envelope({
        'total_files_available': true,
        'storage_stats_available': true,
        'disk_stats_available': true,
        'total_files': 1,
        'total_size': 1,
        'unique_size': 1,
        'dedup_ratio': 1.0,
        'disk_total': 100,
        'disk_used': 1,
        'disk_available': 99,
        'disk_usage_ratio': 0.01,
      });
    }
    if (path == '/api/v1/search' && options.method == 'GET') {
      final query = options.queryParameters['q']! as String;
      final gate = _searchGates[query];
      if (gate != null) {
        // Ignore cancellation deliberately so stale completion is observable.
        await gate.wait();
      }
      if (failNextSearch) {
        failNextSearch = false;
        throw DioException(
          requestOptions: options,
          type: DioExceptionType.connectionError,
          error: 'search connection interrupted',
        );
      }
      return _envelope({
        'query': query,
        'results': [
          {
            'name': '$query.txt',
            'path': '/search/$query.txt',
            'isDir': false,
            'size': 42,
            'modTime': '2026-07-19T12:00:00Z',
          },
        ],
        'count': 1,
      });
    }
    if (path.startsWith('/api/v1/download/') && options.method == 'GET') {
      final range =
          options.headers['Range']?.toString() ??
          options.headers['range']?.toString();
      downloadRanges.add(range);
      final condition =
          options.headers[downloadIdentityConditionHeader]?.toString() ??
          options.headers[downloadIdentityConditionHeader.toLowerCase()]
              ?.toString();
      if (range != null && condition != 'download-identity-1') {
        return _json(412, {
          'code': 'DOWNLOAD_IDENTITY_MISMATCH',
          'message': 'download identity changed',
        });
      }
      if (interruptNextDownload) {
        interruptNextDownload = false;
        late final StreamController<Uint8List> controller;
        controller = StreamController<Uint8List>(
          onListen: () {
            controller.add(
              Uint8List.fromList(downloadPayload.take(3).toList()),
            );
            controller.addError(
              DioException(
                requestOptions: options,
                type: DioExceptionType.connectionError,
                error: 'download connection interrupted',
              ),
            );
            unawaited(controller.close());
          },
        );
        return ResponseBody(
          controller.stream,
          200,
          headers: <String, List<String>>{
            Headers.contentLengthHeader: <String>['${downloadPayload.length}'],
            downloadIdentityHeader: <String>['download-identity-1'],
          },
        );
      }
      final start = range == null
          ? 0
          : int.parse(range.substring('bytes='.length, range.length - 1));
      final bytes = downloadPayload.sublist(start);
      return ResponseBody.fromBytes(
        bytes,
        start == 0 ? 200 : 206,
        headers: <String, List<String>>{
          Headers.contentLengthHeader: <String>['${bytes.length}'],
          downloadIdentityHeader: <String>['download-identity-1'],
          if (start > 0)
            'content-range': <String>[
              'bytes $start-${downloadPayload.length - 1}/'
                  '${downloadPayload.length}',
            ],
        },
      );
    }
    if (path == '/api/v1/upload-sessions' && options.method == 'POST') {
      final gate = _uploadCreateGate;
      _uploadCreateGate = null;
      if (gate != null) {
        await gate.wait();
      }
      if (expireNextUploadCreate) {
        expireNextUploadCreate = false;
        return _json(410, {
          'code': 'UPLOAD_SESSION_EXPIRED',
          'message': 'upload session expired',
        });
      }
      final body = Map<String, dynamic>.from(options.data! as Map);
      final clientRequestId = body['client_request_id']! as String;
      final replay = _uploadSessions.values
          .where((session) => session.clientRequestId == clientRequestId)
          .firstOrNull;
      if (replay != null) {
        return _envelope(replay.toJson());
      }
      final id = 'upload-session-${_nextUploadSession++}';
      final session = _UploadSessionFixture(
        id: id,
        clientRequestId: clientRequestId,
        path: body['path']! as String,
        totalBytes: body['total_bytes']! as int,
      );
      _uploadSessions[id] = session;
      if (disconnectNextUploadCreate) {
        disconnectNextUploadCreate = false;
        throw DioException(
          requestOptions: options,
          type: DioExceptionType.connectionError,
          error: 'upload create response interrupted',
        );
      }
      return _envelope(session.toJson());
    }
    if (path.startsWith('/api/v1/upload-sessions/by-client-request/') &&
        options.method == 'GET') {
      final goneStatus = goneNextUploadLookupStatus;
      goneNextUploadLookupStatus = null;
      if (goneStatus != null) {
        return _json(goneStatus, {
          'code': goneStatus == 404
              ? 'UPLOAD_SESSION_NOT_FOUND'
              : 'UPLOAD_SESSION_EXPIRED',
          'message': goneStatus == 404
              ? 'upload session not found'
              : 'upload session expired',
        });
      }
      final clientRequestId = options.uri.pathSegments.last;
      final session = _uploadSessions.values
          .where((candidate) => candidate.clientRequestId == clientRequestId)
          .firstOrNull;
      if (session == null) {
        return _json(404, {
          'code': 'UPLOAD_SESSION_NOT_FOUND',
          'message': 'upload session not found',
        });
      }
      return _envelope(session.toJson());
    }
    if (path.startsWith('/api/v1/upload-sessions/')) {
      final segments = options.uri.pathSegments;
      final id = segments[3];
      final session = _uploadSessions[id];
      if (session == null) {
        return _json(404, {
          'code': 'UPLOAD_SESSION_NOT_FOUND',
          'message': 'upload session not found',
        });
      }
      if (segments.length == 5 &&
          segments[4] == 'commit' &&
          options.method == 'POST') {
        final goneStatus = goneNextUploadCommitStatus;
        goneNextUploadCommitStatus = null;
        if (goneStatus != null) {
          return _json(goneStatus, {
            'code': goneStatus == 404
                ? 'UPLOAD_SESSION_NOT_FOUND'
                : 'UPLOAD_SESSION_EXPIRED',
            'message': goneStatus == 404
                ? 'upload session not found'
                : 'upload session expired',
          });
        }
        if (conflictNextUploadCommit) {
          conflictNextUploadCommit = false;
          session.state = 'conflict';
          return _json(409, {
            'code': 'CONFLICT',
            'message': 'upload target changed',
          });
        }
        if (session.state == 'ready' || session.state == 'committing') {
          session.state = 'committed';
        }
        return _envelope(session.toJson());
      }
      if (segments.length == 4 && options.method == 'GET') {
        if (expireUploadSessionStatus) {
          return _json(410, {
            'code': 'UPLOAD_SESSION_EXPIRED',
            'message': 'upload session expired',
          });
        }
        return _envelope(session.toJson());
      }
      if (segments.length == 4 && options.method == 'DELETE') {
        final goneStatus = goneNextUploadSessionCancelStatus;
        goneNextUploadSessionCancelStatus = null;
        if (goneStatus != null) {
          return _json(goneStatus, {
            'code': goneStatus == 404
                ? 'UPLOAD_SESSION_NOT_FOUND'
                : 'UPLOAD_SESSION_EXPIRED',
            'message': goneStatus == 404
                ? 'upload session not found'
                : 'upload session expired',
          });
        }
        session.state = 'cancelled';
        return _envelope(session.toJson());
      }
      if (segments.length == 4 && options.method == 'PATCH') {
        final goneStatus = goneNextUploadChunkStatus;
        goneNextUploadChunkStatus = null;
        if (goneStatus != null) {
          return _json(goneStatus, {
            'code': goneStatus == 404
                ? 'UPLOAD_SESSION_NOT_FOUND'
                : 'UPLOAD_SESSION_EXPIRED',
            'message': goneStatus == 404
                ? 'upload session not found'
                : 'upload session expired',
          });
        }
        final offset = int.parse(
          options.headers[uploadOffsetHeader]!.toString(),
        );
        final length = int.parse(
          options.headers[Headers.contentLengthHeader]!.toString(),
        );
        if (offset != session.durableOffset) {
          return _json(409, {
            'code': 'UPLOAD_OFFSET_MISMATCH',
            'message': 'upload offset mismatch',
            'details': {'durable_offset': session.durableOffset},
          });
        }
        session.durableOffset += length;
        if (session.durableOffset == session.totalBytes) {
          session.state = 'ready';
        }
        if (disconnectNextUpload) {
          disconnectNextUpload = false;
          throw DioException(
            requestOptions: options,
            type: DioExceptionType.connectionError,
            error: 'upload connection interrupted',
          );
        }
        return _envelope(session.toJson());
      }
    }
    if (path == '/api/v1/trash/' && options.method == 'GET') {
      final snapshot = trashItems
          .map((item) => Map<String, dynamic>.from(item))
          .toList(growable: false);
      final gate = _trashListGate;
      _trashListGate = null;
      if (gate != null) {
        // Ignore cancellation deliberately so stale completion is observable.
        await gate.wait();
      }
      if (failNextTrashList) {
        failNextTrashList = false;
        throw DioException(
          requestOptions: options,
          type: DioExceptionType.connectionError,
          error: 'trash listing connection interrupted',
        );
      }
      return _envelope(_trashListing(snapshot));
    }
    if (path.endsWith('/restore') && options.method == 'POST') {
      final id = options.uri.pathSegments[options.uri.pathSegments.length - 2];
      trashItems.removeWhere((item) => item['id'] == id);
      if (failNextTrashRestoreAfterMutation) {
        failNextTrashRestoreAfterMutation = false;
        return _json(500, {
          'code': 'TRASH_PERSISTENCE_FAILED',
          'message': 'restore committed before persistence failure',
        });
      }
      if (disconnectNextTrashMutation) {
        disconnectNextTrashMutation = false;
        throw DioException(
          requestOptions: options,
          type: DioExceptionType.connectionError,
          error: 'trash restore connection interrupted',
        );
      }
      return _envelope({'id': id, 'restored': true, 'warning': false});
    }
    if (path == '/api/v1/trash/empty' && options.method == 'POST') {
      final body = Map<String, dynamic>.from(options.data! as Map);
      final selectedIds = List<String>.from(body['ids']! as List);
      final gate = _trashMutationGate;
      _trashMutationGate = null;
      if (gate != null) {
        await gate.wait();
      }
      if (disconnectNextTrashMutation) {
        disconnectNextTrashMutation = false;
        trashItems.removeWhere(
          (item) => disconnectedDeletedIds.contains(item['id']),
        );
        throw DioException(
          requestOptions: options,
          type: DioExceptionType.connectionError,
          error: 'trash mutation connection interrupted',
        );
      }
      if (failNextTrashEmptyAfterMutation) {
        failNextTrashEmptyAfterMutation = false;
        trashItems.removeWhere(
          (item) => failedTrashEmptyDeletedIds.contains(item['id']),
        );
        return _json(500, {
          'code': 'TRASH_BATCH_PERSISTENCE_FAILED',
          'message': 'selection partially committed before persistence failure',
        });
      }
      final result =
          trashEmptyResult ??
          _trashEmptyResult(deleted: selectedIds, remaining: const <String>[]);
      final deleted = Set<String>.from(result['deleted']! as List);
      trashItems.removeWhere((item) => deleted.contains(item['id']));
      return _envelope(result);
    }
    if (path.startsWith('/api/v1/files/')) {
      final suffix = path.substring('/api/v1/files/'.length);
      final logicalPath = suffix.isEmpty ? '/' : '/${Uri.decodeFull(suffix)}';
      final gate = _directoryGates[logicalPath];
      if (gate != null) {
        // Ignore cancellation deliberately so stale completion is observable.
        await gate.wait();
      }
      if (disconnectNextDirectory) {
        disconnectNextDirectory = false;
        throw DioException(
          requestOptions: options,
          type: DioExceptionType.connectionError,
          error: 'directory connection interrupted',
        );
      }
      final entrySuffix = logicalPath == '/'
          ? 'root'
          : logicalPath.substring(1).replaceAll('/', '-');
      return _envelope({
        'path': logicalPath,
        'capabilities': _capabilities(),
        'deleteMode': 'trash',
        'deletePolicyToken': List.filled(64, 'a').join(),
        'trashRetentionDays': 30,
        'trashAutoCleanupEnabled': true,
        'files': [
          {
            'name': 'entry-$entrySuffix',
            'path': logicalPath == '/'
                ? '/entry-$entrySuffix'
                : '$logicalPath/entry-$entrySuffix',
            'isDir': false,
            'size': 1,
            'modTime': '2026-07-19T12:00:00Z',
            'capabilities': _capabilities(),
          },
        ],
      });
    }
    if (path.startsWith('/api/v1/directories/') && options.method == 'POST') {
      final gate = _fileMutationGate;
      _fileMutationGate = null;
      if (gate != null) {
        await gate.wait();
      }
      if (disconnectNextFileMutation) {
        disconnectNextFileMutation = false;
        throw DioException(
          requestOptions: options,
          type: DioExceptionType.connectionError,
          error: 'file mutation connection interrupted',
        );
      }
      final status = nextFileMutationStatus;
      nextFileMutationStatus = null;
      if (status != null) {
        return _json(status, {
          'code': 'INTERNAL_ERROR',
          'message': 'file mutation result is uncertain',
        });
      }
      final suffix = path.substring('/api/v1/directories/'.length);
      return _envelope({
        'path': '/${Uri.decodeFull(suffix)}',
        'warning': false,
      });
    }
    return _json(404, {'code': 'NOT_FOUND', 'message': 'not found'});
  }

  Map<String, dynamic> _user(String username) {
    return {
      'id': 'user-$username',
      'username': username,
      'role': userRole,
      'home_dir': '/',
      'must_change_password': false,
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

final class _ControllerRequest {
  const _ControllerRequest({
    required this.method,
    required this.path,
    required this.query,
    required this.data,
  });

  final String method;
  final String path;
  final Map<String, dynamic> query;
  final Object? data;
}

final class _UploadSessionFixture {
  _UploadSessionFixture({
    required this.id,
    required this.clientRequestId,
    required this.path,
    required this.totalBytes,
  });

  final String id;
  final String clientRequestId;
  final String path;
  final int totalBytes;
  int durableOffset = 0;
  String state = 'uploading';

  Map<String, dynamic> toJson() {
    return <String, dynamic>{
      'id': id,
      'path': path,
      'state': state,
      'durable_offset': durableOffset,
      'total_bytes': totalBytes,
      'created_at': '2026-07-19T12:00:00Z',
      'updated_at': '2026-07-19T12:01:00Z',
      'expires_at': '2026-07-22T12:00:00Z',
      'content_blake3':
          state == 'ready' ||
              state == 'committing' ||
              state == 'committed' ||
              state == 'conflict'
          ? List<String>.filled(64, 'b').join()
          : null,
      'persistence_warning': false,
    };
  }
}

List<Map<String, dynamic>> _defaultTrashItems() {
  return <Map<String, dynamic>>[
    _trashItem(id: 'trash-a', path: '/docs/a.txt', size: 10),
    _trashItem(id: 'trash-b', path: '/docs/b.txt', size: 20),
    _trashItem(id: 'trash-c', path: '/photos/c.jpg', size: 30),
  ];
}

Map<String, dynamic> _trashItem({
  required String id,
  required String path,
  required int size,
}) {
  return <String, dynamic>{
    'id': id,
    'originalPath': path,
    'name': path.split('/').last,
    'isDir': false,
    'size': size,
    'deletedAt': '2026-07-18T12:00:00Z',
    'expiresAt': '2026-08-17T12:00:00Z',
    'hadVersions': false,
  };
}

Map<String, dynamic> _trashListing(List<Map<String, dynamic>> items) {
  return <String, dynamic>{
    'items': items,
    'count': items.length,
    'totalSize': items.fold<int>(
      0,
      (total, item) => total + (item['size']! as int),
    ),
    'retentionDays': 30,
    'retentionEnabled': true,
    'retentionMaxSize': 1024 * 1024,
    'trashAutoCleanupEnabled': true,
  };
}

Map<String, dynamic> _trashEmptyResult({
  required List<String> deleted,
  required List<String> remaining,
  List<String> skipped = const <String>[],
  bool warning = false,
}) {
  return <String, dynamic>{
    'deleted': deleted,
    'remaining': remaining,
    'skipped': skipped,
    'deleted_count': deleted.length,
    'remaining_count': remaining.length,
    'skipped_count': skipped.length,
    'partial': remaining.isNotEmpty || skipped.isNotEmpty,
    'warning': warning,
  };
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
