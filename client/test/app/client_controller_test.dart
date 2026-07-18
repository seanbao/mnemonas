import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/app/client_controller.dart';
import 'package:mnemonas_client/app/client_state.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';
import 'package:mnemonas_client/core/server/server_preferences.dart';
import 'package:mnemonas_client/core/trash/trash_models.dart';

void main() {
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
  }) async {
    final preferences = _FakeServerPreferencesStore(savedEndpoint);
    final activeSessionStore = sessionStore ?? MemoryAuthSessionStore();
    final factory = controllerFactory ?? ClientController.new;
    final container = ProviderContainer(
      overrides: [
        serverPreferencesProvider.overrideWithValue(preferences),
        authSessionStoreProvider.overrideWithValue(activeSessionStore),
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

final class _ControllerAdapter implements HttpClientAdapter {
  _ControllerAdapter({this.userRole = 'admin'})
    : trashItems = _defaultTrashItems();

  final String userRole;
  final List<Map<String, dynamic>> trashItems;
  final List<_ControllerRequest> requests = <_ControllerRequest>[];
  final Map<String, _RequestGate> _loginGates = {};
  final Map<String, _RequestGate> _directoryGates = {};
  _RequestGate? _probeGate;
  _RequestGate? _trashListGate;
  _RequestGate? _trashMutationGate;
  _RequestGate? _passwordChangeGate;
  Map<String, dynamic>? trashEmptyResult;
  bool disconnectNextTrashMutation = false;
  bool disconnectNextPasswordChange = false;
  bool rejectNextPasswordChange = false;
  Set<String> disconnectedDeletedIds = const <String>{};
  bool failNextTrashRestoreAfterMutation = false;
  bool failNextTrashEmptyAfterMutation = false;
  Set<String> failedTrashEmptyDeletedIds = const <String>{};
  bool failNextTrashList = false;

  List<_ControllerRequest> get trashRequests => requests
      .where((request) => request.path.startsWith('/api/v1/trash/'))
      .toList(growable: false);

  List<_ControllerRequest> get trashMutationRequests => trashRequests
      .where((request) => request.method != 'GET')
      .toList(growable: false);

  _RequestGate holdLogin(String username) {
    return _loginGates[username] = _RequestGate();
  }

  _RequestGate holdDirectory(String path) {
    return _directoryGates[path] = _RequestGate();
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
    required this.data,
  });

  final String method;
  final String path;
  final Object? data;
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
