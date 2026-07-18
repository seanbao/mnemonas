import '../core/auth/auth_models.dart';
import '../core/files/file_models.dart';
import '../core/server/server_endpoint.dart';
import '../core/system/system_models.dart';
import '../core/trash/trash_models.dart';

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
    this.trash,
    this.isBusy = false,
    this.isTrashBusy = false,
    this.trashReconciliationRequired = false,
    this.errorMessage,
    this.trashErrorMessage,
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
  final TrashListing? trash;
  final bool isBusy;
  final bool isTrashBusy;
  final bool trashReconciliationRequired;
  final String? errorMessage;
  final String? trashErrorMessage;
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
    Object? trash = _unset,
    bool? isBusy,
    bool? isTrashBusy,
    bool? trashReconciliationRequired,
    Object? errorMessage = _unset,
    Object? trashErrorMessage = _unset,
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
      trash: identical(trash, _unset) ? this.trash : trash as TrashListing?,
      isBusy: isBusy ?? this.isBusy,
      isTrashBusy: isTrashBusy ?? this.isTrashBusy,
      trashReconciliationRequired:
          trashReconciliationRequired ?? this.trashReconciliationRequired,
      errorMessage: identical(errorMessage, _unset)
          ? this.errorMessage
          : errorMessage as String?,
      trashErrorMessage: identical(trashErrorMessage, _unset)
          ? this.trashErrorMessage
          : trashErrorMessage as String?,
      notice: identical(notice, _unset) ? this.notice : notice as String?,
      transfers: transfers ?? this.transfers,
    );
  }
}

const Object _unset = Object();
