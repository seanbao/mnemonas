import '../core/auth/auth_models.dart';
import '../core/files/file_models.dart';
import '../core/server/server_endpoint.dart';
import '../core/system/system_models.dart';

enum ClientStage {
  booting,
  needsConnection,
  unavailable,
  needsLogin,
  mandatoryPasswordChange,
  ready,
}

enum TransferDirection { upload, download }

enum TransferStatus { running, completed, failed, cancelled }

final class ClientTransfer {
  const ClientTransfer({
    required this.id,
    required this.name,
    required this.direction,
    required this.status,
    required this.transferred,
    required this.total,
    this.errorMessage,
  });

  final String id;
  final String name;
  final TransferDirection direction;
  final TransferStatus status;
  final int transferred;
  final int total;
  final String? errorMessage;

  double? get progress {
    if (total <= 0) {
      return null;
    }
    return (transferred / total).clamp(0, 1);
  }

  ClientTransfer copyWith({
    TransferStatus? status,
    int? transferred,
    int? total,
    String? errorMessage,
  }) {
    return ClientTransfer(
      id: id,
      name: name,
      direction: direction,
      status: status ?? this.status,
      transferred: transferred ?? this.transferred,
      total: total ?? this.total,
      errorMessage: errorMessage,
    );
  }
}

final class ClientState {
  const ClientState({
    required this.stage,
    this.endpoint,
    this.probe,
    this.user,
    this.currentPath = '/',
    this.directory,
    this.stats,
    this.isBusy = false,
    this.errorMessage,
    this.notice,
    this.transfers = const <ClientTransfer>[],
  });

  const ClientState.booting() : this(stage: ClientStage.booting);

  final ClientStage stage;
  final ServerEndpoint? endpoint;
  final ServerProbe? probe;
  final AuthUser? user;
  final String currentPath;
  final DirectoryListing? directory;
  final StorageStats? stats;
  final bool isBusy;
  final String? errorMessage;
  final String? notice;
  final List<ClientTransfer> transfers;

  bool get isAuthenticated =>
      stage == ClientStage.ready ||
      stage == ClientStage.mandatoryPasswordChange;

  ClientState copyWith({
    ClientStage? stage,
    Object? endpoint = _unset,
    Object? probe = _unset,
    Object? user = _unset,
    String? currentPath,
    Object? directory = _unset,
    Object? stats = _unset,
    bool? isBusy,
    Object? errorMessage = _unset,
    Object? notice = _unset,
    List<ClientTransfer>? transfers,
  }) {
    return ClientState(
      stage: stage ?? this.stage,
      endpoint: identical(endpoint, _unset)
          ? this.endpoint
          : endpoint as ServerEndpoint?,
      probe: identical(probe, _unset) ? this.probe : probe as ServerProbe?,
      user: identical(user, _unset) ? this.user : user as AuthUser?,
      currentPath: currentPath ?? this.currentPath,
      directory: identical(directory, _unset)
          ? this.directory
          : directory as DirectoryListing?,
      stats: identical(stats, _unset) ? this.stats : stats as StorageStats?,
      isBusy: isBusy ?? this.isBusy,
      errorMessage: identical(errorMessage, _unset)
          ? this.errorMessage
          : errorMessage as String?,
      notice: identical(notice, _unset) ? this.notice : notice as String?,
      transfers: transfers ?? this.transfers,
    );
  }
}

const Object _unset = Object();
