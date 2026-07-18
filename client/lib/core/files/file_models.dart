import 'file_path.dart';

final class FileCapabilities {
  const FileCapabilities({
    required this.read,
    required this.concreteRead,
    required this.write,
  });

  factory FileCapabilities.fromJson(Map<String, dynamic> json) {
    return FileCapabilities(
      read: json['read'] == true,
      concreteRead: json['concreteRead'] == true,
      write: json['write'] == true,
    );
  }

  final bool read;
  final bool concreteRead;
  final bool write;
}

final class FileEntry {
  const FileEntry({
    required this.name,
    required this.path,
    required this.isDirectory,
    required this.size,
    required this.modifiedAt,
    required this.capabilities,
    this.deleteIdentityToken,
    this.contentHash,
    this.versioned = false,
  });

  factory FileEntry.fromJson(Map<String, dynamic> json) {
    final modifiedAt = DateTime.tryParse(_requiredString(json, 'modTime'));
    if (modifiedAt == null) {
      throw const FormatException('Invalid file modification time');
    }
    final size = json['size'];
    if (size is! num || size < 0) {
      throw const FormatException('Invalid file size');
    }

    return FileEntry(
      name: _requiredString(json, 'name'),
      path: _requiredString(json, 'path'),
      isDirectory: json['isDir'] == true,
      size: size.toInt(),
      modifiedAt: modifiedAt.toUtc(),
      deleteIdentityToken: _optionalSha256Token(json, 'deleteIdentityToken'),
      contentHash: _optionalBlake3(json, 'hash'),
      versioned: json['versioned'] == true,
      capabilities: FileCapabilities.fromJson(
        _requireMap(json['capabilities']),
      ),
    );
  }

  final String name;
  final String path;
  final bool isDirectory;
  final int size;
  final DateTime modifiedAt;
  final String? deleteIdentityToken;
  final String? contentHash;
  final bool versioned;
  final FileCapabilities capabilities;

  bool get canPrepareDelete =>
      deleteIdentityToken != null &&
      isLowercaseSha256Token(deleteIdentityToken!);

  bool get hasVerifiableContentIdentity =>
      contentHash != null && _lowercaseDigestPattern.hasMatch(contentHash!);
}

enum DeleteMode {
  trash,
  permanent;

  static DeleteMode parse(Object? value) {
    return switch (value) {
      'trash' => DeleteMode.trash,
      'permanent' => DeleteMode.permanent,
      _ => throw const FormatException('Invalid delete mode'),
    };
  }

  String get wireValue => name;
}

final class DeletePolicySnapshot {
  const DeletePolicySnapshot({
    required this.mode,
    required this.token,
    required this.retentionDays,
    required this.autoCleanupEnabled,
  });

  factory DeletePolicySnapshot.fromJson(Map<String, dynamic> json) {
    final retentionDays = json['trashRetentionDays'];
    if (retentionDays is! int || retentionDays < 0) {
      throw const FormatException('Invalid trash retention period');
    }
    final token = _requiredString(json, 'deletePolicyToken');
    if (!RegExp(r'^[0-9a-f]{64}$').hasMatch(token)) {
      throw const FormatException('Invalid delete policy token');
    }
    final autoCleanupEnabled = json['trashAutoCleanupEnabled'];
    if (autoCleanupEnabled is! bool) {
      throw const FormatException('Invalid trash cleanup state');
    }
    return DeletePolicySnapshot(
      mode: DeleteMode.parse(json['deleteMode']),
      token: token,
      retentionDays: retentionDays,
      autoCleanupEnabled: autoCleanupEnabled,
    );
  }

  final DeleteMode mode;
  final String token;
  final int retentionDays;
  final bool autoCleanupEnabled;
}

final class DirectoryListing {
  const DirectoryListing({
    required this.path,
    required this.capabilities,
    required this.deletePolicy,
    required this.entries,
  });

  factory DirectoryListing.fromJson(Map<String, dynamic> json) {
    final files = json['files'];
    if (files is! List) {
      throw const FormatException('Invalid directory entries');
    }
    return DirectoryListing(
      path: _requiredString(json, 'path'),
      capabilities: FileCapabilities.fromJson(
        _requireMap(json['capabilities']),
      ),
      deletePolicy: DeletePolicySnapshot.fromJson(json),
      entries: files
          .map((item) => FileEntry.fromJson(_requireMap(item)))
          .toList(growable: false),
    );
  }

  final String path;
  final FileCapabilities capabilities;
  final DeletePolicySnapshot deletePolicy;
  final List<FileEntry> entries;
}

final class FileMutationResult {
  const FileMutationResult({
    required this.path,
    required this.persistenceWarning,
  });

  factory FileMutationResult.fromJson(Map<String, dynamic> json) {
    return FileMutationResult(
      path: _requiredString(json, 'path'),
      persistenceWarning: json['warning'] == true,
    );
  }

  final String path;
  final bool persistenceWarning;
}

final class FileVersion {
  FileVersion({
    required this.path,
    required this.sequence,
    required this.hash,
    required this.size,
    required DateTime timestamp,
    this.comment,
  }) : timestamp = timestamp.toUtc() {
    if (normalizeLogicalPath(path, allowRoot: false) != path) {
      throw const FormatException('File version path is not normalized');
    }
    if (sequence <= 0) {
      throw const FormatException('Invalid file version sequence');
    }
    if (!_lowercaseDigestPattern.hasMatch(hash)) {
      throw const FormatException('Invalid file version hash');
    }
    if (size < 0) {
      throw const FormatException('Invalid file version size');
    }
    if (comment != null &&
        (comment!.isEmpty ||
            comment!.length > 256 ||
            comment!.runes.any(
              (rune) =>
                  rune < 0x20 ||
                  rune == 0x7f ||
                  (rune >= 0x202a && rune <= 0x202e) ||
                  (rune >= 0x2066 && rune <= 0x2069),
            ))) {
      throw const FormatException('Invalid file version comment');
    }
  }

  factory FileVersion.fromJson(
    Map<String, dynamic> json, {
    required String path,
    required int expectedSequence,
  }) {
    const requiredKeys = <String>{'version', 'hash', 'size', 'timestamp'};
    const supportedKeys = <String>{...requiredKeys, 'comment'};
    if (requiredKeys.any((key) => !json.containsKey(key)) ||
        json.keys.any((key) => !supportedKeys.contains(key))) {
      throw const FormatException(
        'File version contains missing or unknown fields',
      );
    }

    final sequence = _requiredInt(json, 'version');
    if (sequence != expectedSequence) {
      throw const FormatException('File versions are not in sequence');
    }
    final size = _requiredInt(json, 'size');
    final comment = json.containsKey('comment')
        ? _requiredString(json, 'comment')
        : null;
    return FileVersion(
      path: path,
      sequence: sequence,
      hash: _requiredBlake3(json, 'hash'),
      size: size,
      timestamp: _requiredTimestamp(json, 'timestamp'),
      comment: comment,
    );
  }

  final String path;
  final int sequence;
  final String hash;
  final int size;
  final DateTime timestamp;
  final String? comment;

  bool get isCurrent => sequence == 1;
}

final class FileVersionHistory {
  FileVersionHistory._({
    required this.path,
    required List<FileVersion> versions,
  }) : versions = List.unmodifiable(versions);

  factory FileVersionHistory.fromJson(
    Map<String, dynamic> json, {
    required String expectedPath,
  }) {
    const expectedKeys = <String>{'path', 'versions'};
    if (json.length != expectedKeys.length ||
        expectedKeys.any((key) => !json.containsKey(key))) {
      throw const FormatException(
        'File version history contains missing or unknown fields',
      );
    }

    final path = _requiredString(json, 'path');
    if (path != expectedPath ||
        normalizeLogicalPath(path, allowRoot: false) != path) {
      throw const FormatException(
        'File version history path does not match request',
      );
    }
    final encodedVersions = json['versions'];
    if (encodedVersions is! List || encodedVersions.isEmpty) {
      throw const FormatException('File version history is empty or invalid');
    }
    final versions = <FileVersion>[];
    for (var index = 0; index < encodedVersions.length; index++) {
      versions.add(
        FileVersion.fromJson(
          _requireMap(encodedVersions[index]),
          path: path,
          expectedSequence: index + 1,
        ),
      );
    }
    return FileVersionHistory._(path: path, versions: versions);
  }

  final String path;
  final List<FileVersion> versions;

  FileVersion get current => versions.first;
}

final class VersionRestoreResult {
  const VersionRestoreResult._({
    required this.path,
    required this.restoredHash,
    required this.persistenceWarning,
  });

  factory VersionRestoreResult.fromJson(
    Map<String, dynamic> json, {
    required String expectedPath,
    required String expectedHash,
  }) {
    const requiredKeys = <String>{'path', 'restored'};
    const supportedKeys = <String>{...requiredKeys, 'warning'};
    if (requiredKeys.any((key) => !json.containsKey(key)) ||
        json.keys.any((key) => !supportedKeys.contains(key))) {
      throw const FormatException(
        'Version restore result contains missing or unknown fields',
      );
    }

    final warning = json['warning'];
    if (warning != null && warning is! bool) {
      throw const FormatException('Invalid version restore warning');
    }
    final path = _requiredString(json, 'path');
    final restoredHash = _requiredBlake3(json, 'restored');
    if (path != expectedPath ||
        normalizeLogicalPath(path, allowRoot: false) != path ||
        restoredHash != expectedHash) {
      throw const FormatException(
        'Version restore result does not match request',
      );
    }
    return VersionRestoreResult._(
      path: path,
      restoredHash: restoredHash,
      persistenceWarning: warning == true,
    );
  }

  final String path;
  final String restoredHash;
  final bool persistenceWarning;
}

enum UploadSessionState {
  uploading,
  ready,
  committing,
  committed,
  conflict,
  cancelled;

  static UploadSessionState parse(String value) {
    return switch (value) {
      'uploading' => UploadSessionState.uploading,
      'ready' => UploadSessionState.ready,
      'committing' => UploadSessionState.committing,
      'committed' => UploadSessionState.committed,
      'conflict' => UploadSessionState.conflict,
      'cancelled' => UploadSessionState.cancelled,
      _ => throw const FormatException('Invalid upload session state'),
    };
  }
}

final class UploadSessionSnapshot {
  UploadSessionSnapshot({
    required this.id,
    required this.path,
    required this.state,
    required this.durableOffset,
    required this.totalBytes,
    required DateTime createdAt,
    required DateTime updatedAt,
    required DateTime expiresAt,
    required this.contentBlake3,
    required this.persistenceWarning,
  }) : createdAt = createdAt.toUtc(),
       updatedAt = updatedAt.toUtc(),
       expiresAt = expiresAt.toUtc() {
    _validate();
  }

  factory UploadSessionSnapshot.fromJson(Map<String, dynamic> json) {
    const expectedKeys = <String>{
      'id',
      'path',
      'state',
      'durable_offset',
      'total_bytes',
      'created_at',
      'updated_at',
      'expires_at',
      'content_blake3',
      'persistence_warning',
    };
    if (json.length != expectedKeys.length ||
        expectedKeys.any((key) => !json.containsKey(key))) {
      throw const FormatException(
        'Upload session contains missing or unknown fields',
      );
    }

    return UploadSessionSnapshot(
      id: _requiredString(json, 'id'),
      path: _requiredString(json, 'path'),
      state: UploadSessionState.parse(_requiredString(json, 'state')),
      durableOffset: _requiredInt(json, 'durable_offset'),
      totalBytes: _requiredInt(json, 'total_bytes'),
      createdAt: _requiredTimestamp(json, 'created_at'),
      updatedAt: _requiredTimestamp(json, 'updated_at'),
      expiresAt: _requiredTimestamp(json, 'expires_at'),
      contentBlake3: _optionalBlake3(json, 'content_blake3'),
      persistenceWarning: _requiredBool(json, 'persistence_warning'),
    );
  }

  final String id;
  final String path;
  final UploadSessionState state;
  final int durableOffset;
  final int totalBytes;
  final DateTime createdAt;
  final DateTime updatedAt;
  final DateTime expiresAt;
  final String? contentBlake3;
  final bool persistenceWarning;

  bool get isTerminal =>
      state == UploadSessionState.committed ||
      state == UploadSessionState.conflict ||
      state == UploadSessionState.cancelled;

  void _validate() {
    if (!_uploadSessionIdPattern.hasMatch(id)) {
      throw const FormatException('Invalid upload session id');
    }
    if (normalizeLogicalPath(path, allowRoot: false) != path) {
      throw const FormatException('Upload session path is not normalized');
    }
    if (durableOffset < 0 || totalBytes < 0 || durableOffset > totalBytes) {
      throw const FormatException('Invalid upload session byte range');
    }
    if (updatedAt.isBefore(createdAt) || expiresAt.isBefore(updatedAt)) {
      throw const FormatException('Invalid upload session timestamps');
    }

    final hasCompletePayload =
        state == UploadSessionState.ready ||
        state == UploadSessionState.committing ||
        state == UploadSessionState.committed ||
        state == UploadSessionState.conflict;
    if (hasCompletePayload &&
        (durableOffset != totalBytes || contentBlake3 == null)) {
      throw const FormatException(
        'Completed upload payload state is inconsistent',
      );
    }
    if (state == UploadSessionState.uploading && durableOffset == totalBytes) {
      throw const FormatException(
        'Uploading state requires an incomplete payload',
      );
    }
    if (contentBlake3 != null && durableOffset != totalBytes) {
      throw const FormatException(
        'Upload content digest requires a complete payload',
      );
    }
    if (persistenceWarning && state != UploadSessionState.committed) {
      throw const FormatException(
        'Upload persistence warning requires a committed session',
      );
    }
  }
}

final class PathMutationResult {
  const PathMutationResult({
    required this.sourcePath,
    required this.destinationPath,
    required this.persistenceWarning,
  });

  factory PathMutationResult.fromJson(Map<String, dynamic> json) {
    return PathMutationResult(
      sourcePath: _requiredString(json, 'from'),
      destinationPath: _requiredString(json, 'to'),
      persistenceWarning: json['warning'] == true,
    );
  }

  final String sourcePath;
  final String destinationPath;
  final bool persistenceWarning;
}

final class DeleteTargetObservation {
  DeleteTargetObservation._({required this.path, required this.identityToken});

  factory DeleteTargetObservation.fromFileEntry(FileEntry entry) {
    final identityToken = entry.deleteIdentityToken;
    if (identityToken == null || !isLowercaseSha256Token(identityToken)) {
      throw const FormatException(
        'The file does not have a valid delete identity token',
      );
    }
    return DeleteTargetObservation._(
      path: entry.path,
      identityToken: identityToken,
    );
  }

  final String path;
  final String identityToken;

  Map<String, dynamic> toJson() => {
    'path': path,
    'observedIdentityToken': identityToken,
  };
}

final class DeleteTargetSnapshot {
  const DeleteTargetSnapshot._({
    required this.path,
    required this.name,
    required this.isDirectory,
    required this.size,
    required this.modifiedAt,
    required this.identityToken,
    required this.targetToken,
  });

  factory DeleteTargetSnapshot.fromJson(Map<String, dynamic> json) {
    final modifiedAt = DateTime.tryParse(_requiredString(json, 'modTime'));
    if (modifiedAt == null) {
      throw const FormatException('Invalid delete target modification time');
    }
    final size = json['size'];
    if (size is! int || size < 0) {
      throw const FormatException('Invalid delete target size');
    }
    final isDirectory = json['isDir'];
    if (isDirectory is! bool) {
      throw const FormatException('Invalid delete target type');
    }
    return DeleteTargetSnapshot._(
      path: _requiredString(json, 'path'),
      name: _requiredString(json, 'name'),
      isDirectory: isDirectory,
      size: size,
      modifiedAt: modifiedAt.toUtc(),
      identityToken: _requiredSha256Token(json, 'deleteIdentityToken'),
      targetToken: _requiredSha256Token(json, 'deleteTargetToken'),
    );
  }

  final String path;
  final String name;
  final bool isDirectory;
  final int size;
  final DateTime modifiedAt;
  final String identityToken;
  final String targetToken;
}

final class DeleteIntentSnapshot {
  DeleteIntentSnapshot._({
    required this.serverBaseUrl,
    required this.policy,
    required List<DeleteTargetSnapshot> targets,
  }) : targets = List.unmodifiable(targets);

  factory DeleteIntentSnapshot.fromJson(
    Map<String, dynamic> json, {
    required String serverBaseUrl,
    required List<DeleteTargetObservation> expectedTargets,
  }) {
    final rawTargets = json['targets'];
    if (rawTargets is! List || rawTargets.length != expectedTargets.length) {
      throw const FormatException('Delete intent targets do not match request');
    }

    final targets = rawTargets
        .map((target) => DeleteTargetSnapshot.fromJson(_requireMap(target)))
        .toList(growable: false);
    for (var index = 0; index < targets.length; index++) {
      final target = targets[index];
      final expected = expectedTargets[index];
      if (target.path != expected.path ||
          target.identityToken != expected.identityToken) {
        throw const FormatException(
          'Delete intent target identity does not match request',
        );
      }
    }

    return DeleteIntentSnapshot._(
      serverBaseUrl: serverBaseUrl,
      policy: DeletePolicySnapshot.fromJson(json),
      targets: targets,
    );
  }

  final String serverBaseUrl;
  final DeletePolicySnapshot policy;
  final List<DeleteTargetSnapshot> targets;

  List<DeleteConfirmation> get confirmations => List.unmodifiable(
    targets.map(
      (target) => DeleteConfirmation._(
        serverBaseUrl: serverBaseUrl,
        policy: policy,
        target: target,
      ),
    ),
  );

  DeleteConfirmation confirmationForPath(String path) {
    final matches = targets.where((target) => target.path == path);
    if (matches.length != 1) {
      throw StateError('Path is not part of this delete intent');
    }
    return DeleteConfirmation._(
      serverBaseUrl: serverBaseUrl,
      policy: policy,
      target: matches.single,
    );
  }
}

final class DeleteConfirmation {
  const DeleteConfirmation._({
    required this.serverBaseUrl,
    required this.policy,
    required this.target,
  });

  final String serverBaseUrl;
  final DeletePolicySnapshot policy;
  final DeleteTargetSnapshot target;
}

final class DeleteMutationResult {
  const DeleteMutationResult({required this.path, required this.hasWarning});

  factory DeleteMutationResult.fromJson(Map<String, dynamic> json) {
    return DeleteMutationResult(
      path: _requiredString(json, 'path'),
      hasWarning: json['warning'] == true,
    );
  }

  final String path;
  final bool hasWarning;
}

bool isLowercaseSha256Token(String value) =>
    RegExp(r'^[0-9a-f]{64}$').hasMatch(value);

Map<String, dynamic> _requireMap(Object? value) {
  if (value is Map<String, dynamic>) {
    return value;
  }
  if (value is Map) {
    return Map<String, dynamic>.from(value);
  }
  throw const FormatException('Expected a JSON object');
}

String _requiredString(Map<String, dynamic> json, String key) {
  final value = _optionalString(json[key]);
  if (value == null) {
    throw FormatException('Missing or invalid $key');
  }
  return value;
}

String _requiredSha256Token(Map<String, dynamic> json, String key) {
  final value = _requiredString(json, key);
  if (!isLowercaseSha256Token(value)) {
    throw FormatException('Invalid $key');
  }
  return value;
}

String _requiredBlake3(Map<String, dynamic> json, String key) {
  final value = _requiredString(json, key);
  if (!_lowercaseDigestPattern.hasMatch(value)) {
    throw FormatException('Invalid $key');
  }
  return value;
}

String? _optionalSha256Token(Map<String, dynamic> json, String key) {
  final raw = json[key];
  if (raw == null) {
    return null;
  }
  if (raw is! String || !isLowercaseSha256Token(raw)) {
    throw FormatException('Invalid $key');
  }
  return raw;
}

String? _optionalString(Object? value) {
  if (value is! String || value.isEmpty) {
    return null;
  }
  return value;
}

int _requiredInt(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value is! int) {
    throw FormatException('Missing or invalid $key');
  }
  return value;
}

bool _requiredBool(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value is! bool) {
    throw FormatException('Missing or invalid $key');
  }
  return value;
}

DateTime _requiredTimestamp(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value is! String) {
    throw FormatException('Missing or invalid $key');
  }
  final match = _rfc3339TimestampPattern.firstMatch(value);
  if (match == null) {
    throw FormatException('Missing or invalid $key');
  }
  final year = int.parse(match.group(1)!);
  final month = int.parse(match.group(2)!);
  final day = int.parse(match.group(3)!);
  final hour = int.parse(match.group(4)!);
  final minute = int.parse(match.group(5)!);
  final second = int.parse(match.group(6)!);
  if (month < 1 ||
      month > 12 ||
      day < 1 ||
      day > DateTime.utc(year, month + 1, 0).day ||
      hour > 23 ||
      minute > 59 ||
      second > 59) {
    throw FormatException('Missing or invalid $key');
  }
  final parsed = DateTime.tryParse(value);
  if (parsed == null) {
    throw FormatException('Missing or invalid $key');
  }
  return parsed.toUtc();
}

String? _optionalBlake3(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value == null) {
    return null;
  }
  if (value is! String || !_lowercaseDigestPattern.hasMatch(value)) {
    throw FormatException('Invalid $key');
  }
  return value;
}

final RegExp _uploadSessionIdPattern = RegExp(
  r'^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
);
final RegExp _lowercaseDigestPattern = RegExp(r'^[0-9a-f]{64}$');
final RegExp _rfc3339TimestampPattern = RegExp(
  r'^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})'
  r'(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$',
);
