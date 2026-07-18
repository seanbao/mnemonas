import 'dart:async';
import 'dart:io';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/auth/auth_api.dart';
import '../core/auth/session_store.dart';
import '../core/files/file_models.dart';
import '../core/files/file_path.dart';
import '../core/files/files_api.dart';
import '../core/network/api_client.dart';
import '../core/network/api_error.dart';
import '../core/server/server_endpoint.dart';
import '../core/server/server_preferences.dart';
import '../core/system/system_api.dart';
import '../core/system/system_models.dart';
import 'client_state.dart';

final serverPreferencesProvider = Provider<ServerPreferences>(
  (ref) => ServerPreferences(),
);

final authSessionStoreProvider = Provider<AuthSessionStore>(
  (ref) => SecureAuthSessionStore(),
);

final clientControllerProvider =
    NotifierProvider<ClientController, ClientState>(ClientController.new);

class ClientController extends Notifier<ClientState> {
  late final ServerPreferences _serverPreferences;
  late final AuthSessionStore _sessionStore;

  AuthApi? _auth;
  FilesApi? _files;
  SystemApi? _system;
  var _transferSequence = 0;
  final Map<String, CancelToken> _transferCancellations =
      <String, CancelToken>{};

  DirectoryListing? get currentDirectory => state.directory;

  @override
  ClientState build() {
    _serverPreferences = ref.watch(serverPreferencesProvider);
    _sessionStore = ref.watch(authSessionStoreProvider);
    ref.onDispose(_cancelAllTransfers);
    unawaited(Future<void>.microtask(_bootstrap));
    return const ClientState.booting();
  }

  Future<void> _bootstrap() async {
    final endpoint = await _serverPreferences.load();
    if (endpoint == null) {
      state = const ClientState(stage: ClientStage.needsConnection);
      return;
    }
    await _restoreEndpoint(endpoint);
  }

  Future<void> _restoreEndpoint(ServerEndpoint endpoint) async {
    _configure(endpoint);
    state = ClientState(
      stage: ClientStage.booting,
      endpoint: endpoint,
      isBusy: true,
    );
    try {
      final probe = await _system!.probe();
      if (!probe.setup.authEnabled) {
        state = ClientState(
          stage: ClientStage.unavailable,
          endpoint: endpoint,
          probe: probe,
          errorMessage: '当前服务器未启用用户认证。请先在 Web 管理端启用认证。',
        );
        return;
      }

      final stored = await _sessionStore.load();
      if (stored == null || stored.serverBaseUrl != endpoint.baseUrl) {
        state = ClientState(
          stage: ClientStage.needsLogin,
          endpoint: endpoint,
          probe: probe,
        );
        return;
      }

      try {
        final me = await _auth!.me();
        if (me.data.mustChangePassword) {
          state = ClientState(
            stage: ClientStage.mandatoryPasswordChange,
            endpoint: endpoint,
            probe: probe,
            user: me.data,
          );
          return;
        }
        state = ClientState(
          stage: ClientStage.ready,
          endpoint: endpoint,
          probe: probe,
          user: me.data,
        );
        await refreshOverview();
      } on ApiException catch (error) {
        if (await handleAuthenticationFailure(
          error,
          endpoint: endpoint,
          probe: probe,
        )) {
          return;
        }
        if (error.statusCode == 401) {
          await _resetExpiredSession(endpoint: endpoint, probe: probe);
          return;
        }
        rethrow;
      }
    } on Object catch (error) {
      state = ClientState(
        stage: ClientStage.unavailable,
        endpoint: endpoint,
        errorMessage: clientErrorMessage(error),
      );
    }
  }

  Future<void> connect(String value) async {
    final endpoint = ServerEndpoint.parse(value);
    state = state.copyWith(isBusy: true, errorMessage: null, notice: null);
    try {
      _configure(endpoint);
      final probe = await _system!.probe();
      if (!probe.setup.authEnabled) {
        throw const ApiException(
          kind: ApiFailureKind.local,
          code: 'AUTH_REQUIRED',
          message: 'User authentication must be enabled on the server',
        );
      }

      final previous = await _serverPreferences.load();
      if (previous != endpoint) {
        await _sessionStore.clear();
      }
      await _serverPreferences.save(endpoint);
      state = ClientState(
        stage: ClientStage.needsLogin,
        endpoint: endpoint,
        probe: probe,
        notice: endpoint.usesLanHttp ? '当前连接使用局域网 HTTP，传输内容未加密。' : null,
      );
    } on Object catch (error) {
      state = ClientState(
        stage: ClientStage.needsConnection,
        endpoint: endpoint,
        errorMessage: clientErrorMessage(error),
      );
      rethrow;
    }
  }

  Future<void> login({
    required String username,
    required String password,
  }) async {
    final auth = _requireAuth();
    state = state.copyWith(isBusy: true, errorMessage: null, notice: null);
    try {
      final result = await auth.login(username: username, password: password);
      final nextStage = result.data.user.mustChangePassword
          ? ClientStage.mandatoryPasswordChange
          : ClientStage.ready;
      state = state.copyWith(
        stage: nextStage,
        user: result.data.user,
        isBusy: false,
        notice: result.warnings.isEmpty ? null : '登录已完成，但服务器报告了持久化警告。',
      );
      if (nextStage == ClientStage.ready) {
        await refreshOverview();
      }
    } on Object catch (error) {
      if (!await handleAuthenticationFailure(error)) {
        state = state.copyWith(
          isBusy: false,
          errorMessage: clientErrorMessage(error),
        );
      }
      rethrow;
    }
  }

  Future<void> changePassword({
    required String currentPassword,
    required String newPassword,
  }) async {
    final user = state.user;
    if (user == null) {
      throw StateError('A signed-in user is required');
    }
    state = state.copyWith(isBusy: true, errorMessage: null);
    try {
      await _requireAuth().changePassword(
        currentPassword: currentPassword,
        newPassword: newPassword,
        expectedUserId: user.id,
      );
      state = state.copyWith(
        stage: ClientStage.needsLogin,
        user: null,
        directory: null,
        stats: null,
        isBusy: false,
        notice: '密码已修改。所有设备上的会话均已退出，请使用新密码登录。',
      );
    } on Object catch (error) {
      if (await handleAuthenticationFailure(error)) {
        rethrow;
      }
      final sessionMissing = await _sessionStore.load() == null;
      state = state.copyWith(
        stage: sessionMissing ? ClientStage.needsLogin : state.stage,
        user: sessionMissing ? null : state.user,
        isBusy: false,
        errorMessage: sessionMissing
            ? '无法确认密码修改结果。请先尝试使用新密码登录。'
            : clientErrorMessage(error),
      );
      rethrow;
    }
  }

  Future<void> logout() async {
    _cancelAllTransfers();
    state = state.copyWith(isBusy: true, errorMessage: null);
    String? warning;
    try {
      await _requireAuth().logout();
    } on Object {
      warning = '服务器会话未能确认注销，本机登录信息已清除。';
      await _sessionStore.clear();
    }
    state = ClientState(
      stage: ClientStage.needsLogin,
      endpoint: state.endpoint,
      probe: state.probe,
      notice: warning,
    );
  }

  Future<void> retryConnection() async {
    final endpoint = state.endpoint;
    if (endpoint == null) {
      state = const ClientState(stage: ClientStage.needsConnection);
      return;
    }
    await _restoreEndpoint(endpoint);
  }

  Future<void> changeServer() async {
    _cancelAllTransfers();
    await _sessionStore.clear();
    await _serverPreferences.clear();
    _auth = null;
    _files = null;
    _system = null;
    state = const ClientState(stage: ClientStage.needsConnection);
  }

  Future<void> refreshOverview() async {
    if (state.stage != ClientStage.ready) {
      return;
    }
    await loadDirectory(state.currentPath);
    try {
      final stats = await _system!.stats();
      state = state.copyWith(stats: stats.data);
    } on Object catch (error) {
      if (await handleAuthenticationFailure(error)) {
        return;
      }
      // File access remains usable when optional statistics are unavailable.
      state = state.copyWith(stats: null);
    }
  }

  Future<void> loadDirectory(String path) async {
    final normalized = normalizeLogicalPath(path);
    state = state.copyWith(
      currentPath: normalized,
      directory: null,
      isBusy: true,
      errorMessage: null,
    );
    try {
      final response = await _requireFiles().list(normalized);
      state = state.copyWith(
        currentPath: response.data.path,
        directory: response.data,
        isBusy: false,
        notice: response.warnings.isEmpty
            ? state.notice
            : '目录已加载，但服务器报告了持久化警告。',
      );
    } on Object catch (error) {
      if (!await handleAuthenticationFailure(error)) {
        state = state.copyWith(
          isBusy: false,
          errorMessage: clientErrorMessage(error),
        );
      }
      rethrow;
    }
  }

  Future<void> createDirectory(String name) async {
    final trimmed = name.trim();
    if (trimmed.isEmpty || trimmed.contains('/')) {
      throw const FormatException('文件夹名称不能为空或包含斜杠');
    }
    final target = _joinLogicalPath(state.currentPath, trimmed);
    state = state.copyWith(isBusy: true, errorMessage: null);
    try {
      final result = await _requireFiles().createDirectory(target);
      await loadDirectory(state.currentPath);
      if (result.hasWarnings || result.data.persistenceWarning) {
        state = state.copyWith(notice: '文件夹已创建，但服务器报告了持久化警告。');
      }
    } on Object catch (error) {
      if (!await handleAuthenticationFailure(error)) {
        state = state.copyWith(
          isBusy: false,
          errorMessage: clientErrorMessage(error),
        );
      }
      rethrow;
    }
  }

  Future<void> renameEntry(FileEntry entry, String newName) async {
    state = state.copyWith(isBusy: true, errorMessage: null);
    try {
      final result = await _requireFiles().rename(
        logicalPath: entry.path,
        newName: newName,
      );
      await loadDirectory(state.currentPath);
      if (result.hasWarnings || result.data.persistenceWarning) {
        state = state.copyWith(notice: '项目已重命名，但服务器报告了持久化警告。');
      }
    } on Object catch (error) {
      if (!await handleAuthenticationFailure(error)) {
        state = state.copyWith(
          isBusy: false,
          errorMessage: clientErrorMessage(error),
        );
      }
      rethrow;
    }
  }

  Future<void> moveEntries(
    List<FileEntry> entries,
    String destinationDirectory,
  ) {
    return _relocateEntries(entries, destinationDirectory, copy: false);
  }

  Future<void> copyEntries(
    List<FileEntry> entries,
    String destinationDirectory,
  ) {
    return _relocateEntries(entries, destinationDirectory, copy: true);
  }

  Future<void> _relocateEntries(
    List<FileEntry> entries,
    String destinationDirectory, {
    required bool copy,
  }) async {
    if (entries.isEmpty) {
      throw const FormatException('至少选择一个项目');
    }
    final destination = normalizeLogicalPath(destinationDirectory);
    state = state.copyWith(isBusy: true, errorMessage: null);
    var completed = 0;
    var hasWarnings = false;
    try {
      for (final entry in entries) {
        final target = _joinLogicalPath(destination, entry.name);
        final response = copy
            ? await _requireFiles().copy(
                sourcePath: entry.path,
                destinationPath: target,
              )
            : await _requireFiles().move(
                sourcePath: entry.path,
                destinationPath: target,
              );
        completed++;
        hasWarnings =
            hasWarnings ||
            response.hasWarnings ||
            response.data.persistenceWarning;
      }
      await loadDirectory(state.currentPath);
      if (hasWarnings) {
        state = state.copyWith(
          notice: copy ? '项目已复制，但服务器报告了持久化警告。' : '项目已移动，但服务器报告了持久化警告。',
        );
      }
    } on Object catch (error) {
      if (await handleAuthenticationFailure(error)) {
        rethrow;
      }
      try {
        await loadDirectory(state.currentPath);
      } on Object catch (refreshError) {
        if (!await handleAuthenticationFailure(refreshError)) {
          state = state.copyWith(isBusy: false);
        }
      }
      if (state.stage == ClientStage.needsLogin) {
        rethrow;
      }
      state = state.copyWith(
        errorMessage: completed == 0
            ? clientErrorMessage(error)
            : '已完成 $completed 项，后续操作失败。请刷新目录确认结果。',
      );
      rethrow;
    }
  }

  Future<DeleteIntentSnapshot> prepareDelete(List<FileEntry> entries) async {
    state = state.copyWith(isBusy: true, errorMessage: null);
    try {
      final response = await _requireFiles().prepareDeleteIntent(entries);
      state = state.copyWith(isBusy: false);
      return response.data;
    } on Object catch (error) {
      if (!await handleAuthenticationFailure(error)) {
        state = state.copyWith(
          isBusy: false,
          errorMessage: clientErrorMessage(error),
        );
      }
      rethrow;
    }
  }

  Future<void> deleteConfirmed(DeleteIntentSnapshot snapshot) async {
    state = state.copyWith(isBusy: true, errorMessage: null);
    var completed = 0;
    var hasWarnings = false;
    try {
      for (final confirmation in snapshot.confirmations) {
        final response = await _requireFiles().delete(confirmation);
        completed++;
        hasWarnings =
            hasWarnings || response.hasWarnings || response.data.hasWarning;
      }
      await loadDirectory(state.currentPath);
      if (hasWarnings) {
        state = state.copyWith(notice: '删除已完成，但服务器报告了清理或持久化警告。');
      }
    } on Object catch (error) {
      if (await handleAuthenticationFailure(error)) {
        rethrow;
      }
      try {
        await loadDirectory(state.currentPath);
      } on Object catch (refreshError) {
        if (!await handleAuthenticationFailure(refreshError)) {
          state = state.copyWith(isBusy: false);
        }
      }
      if (state.stage == ClientStage.needsLogin) {
        rethrow;
      }
      state = state.copyWith(
        errorMessage: completed == 0
            ? clientErrorMessage(error)
            : '已删除 $completed 项，后续操作失败。请刷新目录确认结果。',
      );
      rethrow;
    }
  }

  Future<void> uploadFile({
    required String sourcePath,
    required String fileName,
  }) async {
    final target = _joinLogicalPath(state.currentPath, fileName);
    final id = _nextTransferId();
    final cancelToken = CancelToken();
    _transferCancellations[id] = cancelToken;
    _addTransfer(
      ClientTransfer(
        id: id,
        name: fileName,
        direction: TransferDirection.upload,
        status: TransferStatus.running,
        transferred: 0,
        total: -1,
      ),
    );
    try {
      final result = await _requireFiles().uploadFile(
        logicalPath: target,
        sourcePath: sourcePath,
        cancelToken: cancelToken,
        onProgress: (transferred, total) {
          _updateTransfer(id, transferred: transferred, total: total);
        },
      );
      _updateTransfer(id, status: TransferStatus.completed);
      await loadDirectory(state.currentPath);
      if (result.hasWarnings || result.data.persistenceWarning) {
        state = state.copyWith(notice: '文件已上传，但服务器报告了持久化警告。');
      }
    } on Object catch (error) {
      if (!await handleAuthenticationFailure(error)) {
        final cancelled =
            error is ApiException && error.kind == ApiFailureKind.cancelled;
        _updateTransfer(
          id,
          status: cancelled ? TransferStatus.cancelled : TransferStatus.failed,
          errorMessage: cancelled ? null : clientErrorMessage(error),
        );
      }
      rethrow;
    } finally {
      _transferCancellations.remove(id);
    }
  }

  Future<File> downloadFile({
    required FileEntry entry,
    required String destinationPath,
    bool overwrite = false,
  }) async {
    final id = _nextTransferId();
    final cancelToken = CancelToken();
    _transferCancellations[id] = cancelToken;
    _addTransfer(
      ClientTransfer(
        id: id,
        name: entry.name,
        direction: TransferDirection.download,
        status: TransferStatus.running,
        transferred: 0,
        total: entry.size,
      ),
    );
    try {
      final result = await _requireFiles().downloadFile(
        logicalPath: entry.path,
        destinationPath: destinationPath,
        overwrite: overwrite,
        cancelToken: cancelToken,
        onProgress: (transferred, total) {
          _updateTransfer(id, transferred: transferred, total: total);
        },
      );
      _updateTransfer(
        id,
        status: TransferStatus.completed,
        transferred: result.bytesWritten,
        total: result.bytesWritten,
      );
      if (result.warnings.isNotEmpty) {
        state = state.copyWith(notice: '文件已下载，但服务器报告了持久化警告。');
      }
      return result.file;
    } on Object catch (error) {
      if (!await handleAuthenticationFailure(error)) {
        final cancelled =
            error is ApiException && error.kind == ApiFailureKind.cancelled;
        _updateTransfer(
          id,
          status: cancelled ? TransferStatus.cancelled : TransferStatus.failed,
          errorMessage: cancelled ? null : clientErrorMessage(error),
        );
      }
      rethrow;
    } finally {
      _transferCancellations.remove(id);
    }
  }

  void cancelTransfer(String id) {
    final token = _transferCancellations[id];
    if (token == null || token.isCancelled) {
      return;
    }
    token.cancel('Cancelled by the user');
    _updateTransfer(id, status: TransferStatus.cancelled);
  }

  @visibleForTesting
  Future<bool> handleAuthenticationFailure(
    Object error, {
    ServerEndpoint? endpoint,
    ServerProbe? probe,
  }) async {
    if (!_isTerminalAuthenticationFailure(error)) {
      return false;
    }
    if (state.stage == ClientStage.needsLogin && state.user == null) {
      return true;
    }
    await _resetExpiredSession(endpoint: endpoint, probe: probe);
    return true;
  }

  Future<void> _resetExpiredSession({
    ServerEndpoint? endpoint,
    ServerProbe? probe,
  }) async {
    _cancelAllTransfers();
    try {
      await ref.read(authSessionStoreProvider).clear();
    } on Object {
      // The in-memory state must still stop using credentials that the server
      // has rejected, even when local secure storage is temporarily unavailable.
    }
    state = ClientState(
      stage: ClientStage.needsLogin,
      endpoint: endpoint ?? state.endpoint,
      probe: probe ?? state.probe,
      errorMessage: '登录状态已失效，请重新登录。',
    );
  }

  void clearMessage() {
    state = state.copyWith(errorMessage: null, notice: null);
  }

  void _cancelAllTransfers() {
    for (final token in _transferCancellations.values.toList(growable: false)) {
      if (!token.isCancelled) {
        token.cancel('Client session ended');
      }
    }
    _transferCancellations.clear();
  }

  void _configure(ServerEndpoint endpoint) {
    final client = ApiClient(endpoint: endpoint, sessionStore: _sessionStore);
    _auth = AuthApi(client);
    _files = FilesApi(client);
    _system = SystemApi(client);
  }

  AuthApi _requireAuth() {
    final auth = _auth;
    if (auth == null) {
      throw StateError('The server is not configured');
    }
    return auth;
  }

  FilesApi _requireFiles() {
    final files = _files;
    if (files == null) {
      throw StateError('The server is not configured');
    }
    return files;
  }

  String _nextTransferId() {
    _transferSequence++;
    return '${DateTime.now().microsecondsSinceEpoch}-$_transferSequence';
  }

  void _addTransfer(ClientTransfer transfer) {
    state = state.copyWith(
      transfers: List<ClientTransfer>.unmodifiable(<ClientTransfer>[
        transfer,
        ...state.transfers.take(19),
      ]),
    );
  }

  void _updateTransfer(
    String id, {
    TransferStatus? status,
    int? transferred,
    int? total,
    String? errorMessage,
  }) {
    state = state.copyWith(
      transfers: List<ClientTransfer>.unmodifiable(
        state.transfers.map(
          (transfer) => transfer.id == id
              ? transfer.copyWith(
                  status: status,
                  transferred: transferred,
                  total: total,
                  errorMessage: errorMessage,
                )
              : transfer,
        ),
      ),
    );
  }
}

bool _isTerminalAuthenticationFailure(Object error) {
  if (error is! ApiException) {
    return false;
  }
  return const <String>{
    'AUTH_SESSION_MISSING',
    'AUTH_SCOPE_CHANGED',
    'INVALID_AUTH_HEADER',
    'INVALID_TOKEN',
    'MISSING_AUTH_HEADER',
    'NOT_AUTHENTICATED',
    'TOKEN_EXPIRED',
    'TOKEN_REVOKED',
    'USER_DISABLED',
    'USER_NOT_FOUND',
  }.contains(error.code);
}

String clientErrorMessage(Object error) {
  if (error is FormatException) {
    return error.message;
  }
  if (error is ApiException) {
    return switch (error.code) {
      'INVALID_CREDENTIALS' || 'INVALID_PASSWORD' => '用户名或密码不正确。',
      'LOGIN_RATE_LIMITED' => '登录尝试过于频繁，请稍后再试。',
      'PASSWORD_CHANGE_REQUIRED' => '需要先修改密码。',
      'AUTH_SESSION_MISSING' ||
      'AUTH_SCOPE_CHANGED' ||
      'INVALID_AUTH_HEADER' ||
      'INVALID_TOKEN' ||
      'MISSING_AUTH_HEADER' ||
      'NOT_AUTHENTICATED' ||
      'TOKEN_EXPIRED' ||
      'TOKEN_REVOKED' ||
      'USER_DISABLED' ||
      'USER_NOT_FOUND' => '登录状态已失效，请重新登录。',
      'WRITE_BUSY' => '设备正在处理其他写入，请稍后重试。',
      'QUOTA_EXCEEDED' => '可用配额不足，无法完成操作。',
      'INSUFFICIENT_STORAGE' => '设备可用空间不足。',
      'CONFLICT' => '文件已发生变化，请刷新后重试。',
      'DELETE_TARGET_CHANGED' ||
      'DELETE_POLICY_CHANGED' => '文件或删除策略已变化，请刷新后重新确认。',
      'AUTH_REQUIRED' => '当前服务器未启用用户认证。',
      _ when error.kind == ApiFailureKind.cancelled => '传输已取消。',
      _ when error.kind == ApiFailureKind.timeout => '连接超时，请确认网络和设备状态。',
      _ when error.kind == ApiFailureKind.connection => '无法连接到设备，请确认地址和网络。',
      _ => error.message,
    };
  }
  if (error is FileSystemException) {
    return '无法访问本地文件，请检查存储权限和可用空间。';
  }
  return '操作未完成，请稍后重试。';
}

String _joinLogicalPath(String parent, String name) {
  final prefix = parent == '/' ? '' : normalizeLogicalPath(parent);
  return normalizeLogicalPath('$prefix/$name', allowRoot: false);
}
