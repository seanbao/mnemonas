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
      contentHash: _optionalString(json['hash']),
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
