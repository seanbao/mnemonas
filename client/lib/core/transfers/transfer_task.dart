import '../files/file_path.dart';
import '../server/server_endpoint.dart';

enum TransferDirection { upload, download }

enum TransferPhase {
  queued,
  running,
  paused,
  awaitingAuth,
  awaitingDestination,
  resultUnconfirmed,
  completed,
  failed,
  cancelled,
}

final class TransferTask {
  TransferTask({
    this.schemaVersion = currentSchemaVersion,
    required this.id,
    required this.direction,
    required this.phase,
    required this.endpointBaseUrl,
    required this.userId,
    required this.remotePath,
    required this.displayName,
    required this.stagingPath,
    this.destinationPath,
    this.destinationUri,
    this.validator,
    required this.durableOffset,
    required this.totalBytes,
    required DateTime createdAt,
    required DateTime updatedAt,
    this.errorCode,
    this.errorMessage,
  }) : createdAt = createdAt.toUtc(),
       updatedAt = updatedAt.toUtc() {
    _validate();
  }

  factory TransferTask.fromJson(Object? value) {
    final json = _requireExactMap(value, _jsonKeys, 'transfer task');
    return TransferTask(
      schemaVersion: _requireInt(json, 'schema_version'),
      id: _requireString(json, 'id'),
      direction: _parseDirection(_requireString(json, 'direction')),
      phase: _parsePhase(_requireString(json, 'phase')),
      endpointBaseUrl: _requireString(json, 'endpoint_base_url'),
      userId: _requireString(json, 'user_id'),
      remotePath: _requireString(json, 'remote_path'),
      displayName: _requireString(json, 'display_name'),
      stagingPath: _requireString(json, 'staging_path'),
      destinationPath: _optionalString(json, 'destination_path'),
      destinationUri: _optionalString(json, 'destination_uri'),
      validator: _optionalString(json, 'validator'),
      durableOffset: _requireInt(json, 'durable_offset'),
      totalBytes: _requireInt(json, 'total_bytes'),
      createdAt: _parseUtcTimestamp(
        _requireString(json, 'created_at'),
        'created_at',
      ),
      updatedAt: _parseUtcTimestamp(
        _requireString(json, 'updated_at'),
        'updated_at',
      ),
      errorCode: _optionalString(json, 'error_code'),
      errorMessage: _optionalString(json, 'error_message'),
    );
  }

  static const int currentSchemaVersion = 1;

  static const Set<String> _jsonKeys = <String>{
    'schema_version',
    'id',
    'direction',
    'phase',
    'endpoint_base_url',
    'user_id',
    'remote_path',
    'display_name',
    'staging_path',
    'destination_path',
    'destination_uri',
    'validator',
    'durable_offset',
    'total_bytes',
    'created_at',
    'updated_at',
    'error_code',
    'error_message',
  };

  final int schemaVersion;
  final String id;
  final TransferDirection direction;
  final TransferPhase phase;
  final String endpointBaseUrl;
  final String userId;
  final String remotePath;
  final String displayName;
  final String stagingPath;
  final String? destinationPath;
  final String? destinationUri;
  final String? validator;
  final int durableOffset;
  final int totalBytes;
  final DateTime createdAt;
  final DateTime updatedAt;
  final String? errorCode;
  final String? errorMessage;

  bool get isTerminal =>
      phase == TransferPhase.completed ||
      phase == TransferPhase.failed ||
      phase == TransferPhase.resultUnconfirmed ||
      phase == TransferPhase.cancelled;

  Map<String, Object?> toJson() => <String, Object?>{
    'schema_version': schemaVersion,
    'id': id,
    'direction': direction.name,
    'phase': phase.name,
    'endpoint_base_url': endpointBaseUrl,
    'user_id': userId,
    'remote_path': remotePath,
    'display_name': displayName,
    'staging_path': stagingPath,
    'destination_path': destinationPath,
    'destination_uri': destinationUri,
    'validator': validator,
    'durable_offset': durableOffset,
    'total_bytes': totalBytes,
    'created_at': createdAt.toIso8601String(),
    'updated_at': updatedAt.toIso8601String(),
    'error_code': errorCode,
    'error_message': errorMessage,
  };

  TransferTask copyWith({
    TransferPhase? phase,
    Object? destinationPath = _unset,
    Object? destinationUri = _unset,
    Object? validator = _unset,
    int? durableOffset,
    DateTime? updatedAt,
    Object? errorCode = _unset,
    Object? errorMessage = _unset,
  }) {
    return TransferTask(
      schemaVersion: schemaVersion,
      id: id,
      direction: direction,
      phase: phase ?? this.phase,
      endpointBaseUrl: endpointBaseUrl,
      userId: userId,
      remotePath: remotePath,
      displayName: displayName,
      stagingPath: stagingPath,
      destinationPath: identical(destinationPath, _unset)
          ? this.destinationPath
          : destinationPath as String?,
      destinationUri: identical(destinationUri, _unset)
          ? this.destinationUri
          : destinationUri as String?,
      validator: identical(validator, _unset)
          ? this.validator
          : validator as String?,
      durableOffset: durableOffset ?? this.durableOffset,
      totalBytes: totalBytes,
      createdAt: createdAt,
      updatedAt: updatedAt ?? this.updatedAt,
      errorCode: identical(errorCode, _unset)
          ? this.errorCode
          : errorCode as String?,
      errorMessage: identical(errorMessage, _unset)
          ? this.errorMessage
          : errorMessage as String?,
    );
  }

  bool hasSameIdentityAs(TransferTask other) {
    return schemaVersion == other.schemaVersion &&
        id == other.id &&
        direction == other.direction &&
        endpointBaseUrl == other.endpointBaseUrl &&
        userId == other.userId &&
        remotePath == other.remotePath &&
        displayName == other.displayName &&
        stagingPath == other.stagingPath &&
        totalBytes == other.totalBytes &&
        createdAt == other.createdAt;
  }

  void _validate() {
    if (schemaVersion != currentSchemaVersion) {
      throw const FormatException('Unsupported transfer task schema');
    }
    _validateIdentifier(id, 'id', maxLength: 128);
    _validateIdentifier(userId, 'user_id', maxLength: 256);
    _validateEndpoint(endpointBaseUrl);
    _validateRemotePath(remotePath);
    _validateDisplayName(displayName);
    _validateAbsoluteLocalPath(stagingPath, 'staging_path');
    if (destinationPath case final path?) {
      _validateAbsoluteLocalPath(path, 'destination_path');
    }
    if (destinationUri case final uri?) {
      _validateContentUri(uri);
    }
    if (validator case final value?) {
      _validateCleanText(value, 'validator', maxLength: 2048);
    }
    if (durableOffset < 0 || totalBytes < 0 || durableOffset > totalBytes) {
      throw const FormatException('Invalid transfer byte range');
    }
    if (createdAt.isAfter(updatedAt)) {
      throw const FormatException(
        'Transfer updated_at must not precede created_at',
      );
    }

    final hasErrorCode = errorCode != null;
    final hasErrorMessage = errorMessage != null;
    if (hasErrorCode != hasErrorMessage) {
      throw const FormatException(
        'Transfer error_code and error_message must appear together',
      );
    }
    if (errorCode case final code?) {
      _validateIdentifier(code, 'error_code', maxLength: 128);
      _validateCleanText(errorMessage!, 'error_message', maxLength: 4096);
    }

    if (direction == TransferDirection.upload &&
        (destinationPath != null || destinationUri != null)) {
      throw const FormatException(
        'Upload tasks must not contain a destination',
      );
    }
    if (destinationPath != null && destinationUri != null) {
      throw const FormatException(
        'Download tasks must contain only one destination',
      );
    }
    if (phase == TransferPhase.awaitingDestination) {
      if (direction != TransferDirection.download ||
          destinationPath != null ||
          durableOffset != totalBytes) {
        throw const FormatException(
          'Awaiting-destination tasks require a complete staged download',
        );
      }
    }
    if (phase == TransferPhase.completed) {
      if (durableOffset != totalBytes ||
          errorCode != null ||
          (direction == TransferDirection.download &&
              destinationPath == null &&
              destinationUri == null)) {
        throw const FormatException('Invalid completed transfer task');
      }
    }
    if (phase == TransferPhase.cancelled && errorCode != null) {
      throw const FormatException(
        'Cancelled transfer tasks must not contain an error',
      );
    }
    if ((phase == TransferPhase.failed ||
            phase == TransferPhase.resultUnconfirmed) &&
        errorCode == null) {
      throw const FormatException(
        'Failed or unconfirmed transfers require an error',
      );
    }
  }

  @override
  bool operator ==(Object other) {
    return other is TransferTask &&
        schemaVersion == other.schemaVersion &&
        id == other.id &&
        direction == other.direction &&
        phase == other.phase &&
        endpointBaseUrl == other.endpointBaseUrl &&
        userId == other.userId &&
        remotePath == other.remotePath &&
        displayName == other.displayName &&
        stagingPath == other.stagingPath &&
        destinationPath == other.destinationPath &&
        destinationUri == other.destinationUri &&
        validator == other.validator &&
        durableOffset == other.durableOffset &&
        totalBytes == other.totalBytes &&
        createdAt == other.createdAt &&
        updatedAt == other.updatedAt &&
        errorCode == other.errorCode &&
        errorMessage == other.errorMessage;
  }

  @override
  int get hashCode => Object.hash(
    schemaVersion,
    id,
    direction,
    phase,
    endpointBaseUrl,
    userId,
    remotePath,
    displayName,
    stagingPath,
    destinationPath,
    destinationUri,
    validator,
    durableOffset,
    totalBytes,
    createdAt,
    updatedAt,
    errorCode,
    errorMessage,
  );
}

const Object _unset = Object();

Map<String, dynamic> _requireExactMap(
  Object? value,
  Set<String> expectedKeys,
  String label,
) {
  if (value is! Map) {
    throw FormatException('$label must be a JSON object');
  }
  if (value.keys.any((key) => key is! String)) {
    throw FormatException('$label contains a non-string key');
  }
  final json = Map<String, dynamic>.from(value);
  if (json.length != expectedKeys.length ||
      expectedKeys.any((key) => !json.containsKey(key))) {
    throw FormatException('$label contains missing or unknown fields');
  }
  return json;
}

String _requireString(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value is! String) {
    throw FormatException('$key must be a string');
  }
  return value;
}

String? _optionalString(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value != null && value is! String) {
    throw FormatException('$key must be a string or null');
  }
  return value as String?;
}

int _requireInt(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value is! int) {
    throw FormatException('$key must be an integer');
  }
  return value;
}

TransferDirection _parseDirection(String value) {
  return switch (value) {
    'upload' => TransferDirection.upload,
    'download' => TransferDirection.download,
    _ => throw const FormatException('Invalid transfer direction'),
  };
}

TransferPhase _parsePhase(String value) {
  return switch (value) {
    'queued' => TransferPhase.queued,
    'running' => TransferPhase.running,
    'paused' => TransferPhase.paused,
    'awaitingAuth' => TransferPhase.awaitingAuth,
    'awaitingDestination' => TransferPhase.awaitingDestination,
    'resultUnconfirmed' => TransferPhase.resultUnconfirmed,
    'completed' => TransferPhase.completed,
    'failed' => TransferPhase.failed,
    'cancelled' => TransferPhase.cancelled,
    _ => throw const FormatException('Invalid transfer phase'),
  };
}

void _validateEndpoint(String value) {
  _validateCleanText(value, 'endpoint_base_url', maxLength: 2048);
  final normalized = ServerEndpoint.parse(
    value,
    allowInsecurePublicHttp: true,
  ).baseUrl;
  if (normalized != value) {
    throw const FormatException('Transfer endpoint must be normalized');
  }
}

void _validateRemotePath(String value) {
  _validateCleanText(value, 'remote_path', maxLength: 4096);
  if (normalizeLogicalPath(value, allowRoot: false) != value) {
    throw const FormatException('Transfer remote path must be normalized');
  }
}

void _validateDisplayName(String value) {
  _validateCleanText(value, 'display_name', maxLength: 255);
  if (validateLogicalName(value) != value) {
    throw const FormatException('Invalid transfer display name');
  }
}

void _validateIdentifier(String value, String field, {required int maxLength}) {
  _validateCleanText(value, field, maxLength: maxLength);
  if (!RegExp(r'^[A-Za-z0-9][A-Za-z0-9._:-]*$').hasMatch(value)) {
    throw FormatException('Invalid transfer $field');
  }
}

void _validateCleanText(String value, String field, {required int maxLength}) {
  if (value.isEmpty ||
      value.trim() != value ||
      value.runes.length > maxLength ||
      value.runes.any((rune) => rune < 0x20 || rune == 0x7f)) {
    throw FormatException('Invalid transfer $field');
  }
}

void _validateAbsoluteLocalPath(String value, String field) {
  _validateCleanText(value, field, maxLength: 4096);
  final segments = value.replaceAll(r'\', '/').split('/');
  if (segments.any((segment) => segment == '.' || segment == '..')) {
    throw FormatException('Transfer $field must be normalized');
  }
  final isUnixAbsolute = value.startsWith('/');
  final isDriveAbsolute = RegExp(r'^[A-Za-z]:[\\/]').hasMatch(value);
  final isUncAbsolute = value.startsWith(r'\\') || value.startsWith('//');
  if (!isUnixAbsolute && !isDriveAbsolute && !isUncAbsolute) {
    throw FormatException('Transfer $field must be absolute');
  }
}

void _validateContentUri(String value) {
  _validateCleanText(value, 'destination_uri', maxLength: 8192);
  final parsed = Uri.tryParse(value);
  if (parsed == null ||
      parsed.scheme != 'content' ||
      !parsed.hasAuthority ||
      parsed.hasFragment) {
    throw const FormatException('Invalid transfer destination_uri');
  }
}

DateTime _parseUtcTimestamp(String value, String field) {
  final match = RegExp(
    r'^(\d{4})-(\d{2})-(\d{2})T'
    r'(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,6}))?Z$',
  ).firstMatch(value);
  if (match == null) {
    throw FormatException('Invalid transfer $field');
  }
  final year = int.parse(match.group(1)!);
  final month = int.parse(match.group(2)!);
  final day = int.parse(match.group(3)!);
  final hour = int.parse(match.group(4)!);
  final minute = int.parse(match.group(5)!);
  final second = int.parse(match.group(6)!);
  final fraction = (match.group(7) ?? '').padRight(6, '0');
  final microseconds = fraction.isEmpty ? 0 : int.parse(fraction);
  if (year == 0) {
    throw FormatException('Invalid transfer $field');
  }
  final parsed = DateTime.utc(
    year,
    month,
    day,
    hour,
    minute,
    second,
    microseconds ~/ 1000,
    microseconds % 1000,
  );
  if (parsed.year != year ||
      parsed.month != month ||
      parsed.day != day ||
      parsed.hour != hour ||
      parsed.minute != minute ||
      parsed.second != second ||
      parsed.millisecond * 1000 + parsed.microsecond != microseconds) {
    throw FormatException('Invalid transfer $field');
  }
  return parsed;
}
