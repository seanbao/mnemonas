import '../files/file_path.dart';

const int maxTrashSelectionIds = 1000;
const int maxTrashItemIdLength = 128;

final class TrashItem {
  const TrashItem._({
    required this.id,
    required this.originalPath,
    required this.name,
    required this.isDirectory,
    required this.size,
    required this.deletedAt,
    required this.expiresAt,
    required this.hadVersions,
  });

  factory TrashItem.fromJson(Map<String, dynamic> json) {
    final id = validateTrashItemId(json['id']);
    final originalPath = _requiredCanonicalPath(json, 'originalPath');
    final name = _requiredString(json, 'name');
    if (validateLogicalName(name) != name ||
        originalPath.split('/').last != name) {
      throw const FormatException(
        'Trash item name does not match its original path',
      );
    }

    final deletedAt = _requiredRfc3339Timestamp(json, 'deletedAt');
    final expiresAt = _requiredRfc3339Timestamp(json, 'expiresAt');
    if (expiresAt.isBefore(deletedAt)) {
      throw const FormatException(
        'Trash item expiry precedes its deletion time',
      );
    }

    return TrashItem._(
      id: id,
      originalPath: originalPath,
      name: name,
      isDirectory: _requiredBool(json, 'isDir'),
      size: _requiredNonNegativeInt(json, 'size'),
      deletedAt: deletedAt,
      expiresAt: expiresAt,
      hadVersions: _requiredBool(json, 'hadVersions'),
    );
  }

  final String id;
  final String originalPath;
  final String name;
  final bool isDirectory;
  final int size;
  final DateTime deletedAt;
  final DateTime expiresAt;
  final bool hadVersions;
}

final class TrashPolicySnapshot {
  const TrashPolicySnapshot._({
    required this.retentionDays,
    required this.retentionEnabled,
    required this.retentionMaxSize,
    required this.autoCleanupEnabled,
  });

  factory TrashPolicySnapshot.fromJson(Map<String, dynamic> json) {
    return TrashPolicySnapshot._(
      retentionDays: _requiredNonNegativeInt(json, 'retentionDays'),
      retentionEnabled: _requiredBool(json, 'retentionEnabled'),
      retentionMaxSize: _requiredNonNegativeInt(json, 'retentionMaxSize'),
      autoCleanupEnabled: _requiredBool(json, 'trashAutoCleanupEnabled'),
    );
  }

  final int retentionDays;
  final bool retentionEnabled;
  final int retentionMaxSize;
  final bool autoCleanupEnabled;
}

final class TrashListing {
  TrashListing._({
    required List<TrashItem> items,
    required this.totalSize,
    required this.policy,
  }) : items = List<TrashItem>.unmodifiable(items);

  factory TrashListing.fromJson(Map<String, dynamic> json) {
    final rawItems = json['items'];
    if (rawItems is! List) {
      throw const FormatException('Invalid trash item list');
    }
    final items = rawItems
        .map((value) => TrashItem.fromJson(_requireMap(value)))
        .toList(growable: false);
    final ids = <String>{};
    for (final item in items) {
      if (!ids.add(item.id)) {
        throw const FormatException('Trash item IDs must be unique');
      }
    }

    final count = _requiredNonNegativeInt(json, 'count');
    if (count != items.length) {
      throw const FormatException('Trash item count does not match the list');
    }
    final totalSize = _requiredNonNegativeInt(json, 'totalSize');
    final computedTotal = items.fold<int>(0, (sum, item) => sum + item.size);
    if (totalSize != computedTotal) {
      throw const FormatException('Trash total size does not match the list');
    }

    return TrashListing._(
      items: items,
      totalSize: totalSize,
      policy: TrashPolicySnapshot.fromJson(json),
    );
  }

  final List<TrashItem> items;
  final int totalSize;
  final TrashPolicySnapshot policy;

  int get count => items.length;

  TrashListing withoutIds(Iterable<String> values) {
    final removed = values.map(validateTrashItemId).toSet();
    final remaining = items
        .where((item) => !removed.contains(item.id))
        .toList(growable: false);
    return TrashListing._(
      items: remaining,
      totalSize: remaining.fold<int>(0, (sum, item) => sum + item.size),
      policy: policy,
    );
  }
}

final class TrashSelectionSnapshot {
  TrashSelectionSnapshot._(List<String> ids)
    : ids = List<String>.unmodifiable(ids);

  factory TrashSelectionSnapshot.fromIds(Iterable<String> values) {
    final ids = values.map(validateTrashItemId).toList(growable: false);
    if (ids.isEmpty || ids.length > maxTrashSelectionIds) {
      throw const FormatException(
        'Trash selection must contain between 1 and 1000 IDs',
      );
    }
    if (ids.toSet().length != ids.length) {
      throw const FormatException('Trash selection IDs must be unique');
    }
    return TrashSelectionSnapshot._(ids);
  }

  factory TrashSelectionSnapshot.fromItems(Iterable<TrashItem> items) {
    return TrashSelectionSnapshot.fromIds(items.map((item) => item.id));
  }

  final List<String> ids;

  Map<String, dynamic> toJson() => {'ids': ids};
}

final class TrashRestoreResult {
  const TrashRestoreResult._({
    required this.id,
    required this.persistenceWarning,
  });

  factory TrashRestoreResult.fromJson(
    Map<String, dynamic> json, {
    required String expectedId,
  }) {
    final id = validateTrashItemId(json['id']);
    if (id != validateTrashItemId(expectedId)) {
      throw const FormatException(
        'Restored trash item does not match the request',
      );
    }
    if (json['restored'] != true) {
      throw const FormatException('Trash restore was not confirmed');
    }
    return TrashRestoreResult._(
      id: id,
      persistenceWarning: _optionalWarning(json),
    );
  }

  final String id;
  final bool persistenceWarning;
}

final class TrashDeleteResult {
  const TrashDeleteResult._({required this.id, required this.cleanupWarning});

  factory TrashDeleteResult.fromJson(
    Map<String, dynamic> json, {
    required String expectedId,
  }) {
    final id = validateTrashItemId(json['id']);
    if (id != validateTrashItemId(expectedId)) {
      throw const FormatException(
        'Deleted trash item does not match the request',
      );
    }
    if (json['deleted'] != true) {
      throw const FormatException('Trash deletion was not confirmed');
    }
    return TrashDeleteResult._(id: id, cleanupWarning: _optionalWarning(json));
  }

  final String id;
  final bool cleanupWarning;
}

final class TrashEmptyResult {
  TrashEmptyResult._({
    required List<String> deleted,
    required List<String> remaining,
    required List<String> skipped,
    required this.partial,
    required this.cleanupWarning,
  }) : deleted = List<String>.unmodifiable(deleted),
       remaining = List<String>.unmodifiable(remaining),
       skipped = List<String>.unmodifiable(skipped);

  factory TrashEmptyResult.fromJson(
    Map<String, dynamic> json, {
    required TrashSelectionSnapshot selection,
  }) {
    final deleted = _requiredIdList(json, 'deleted');
    final remaining = _requiredIdList(json, 'remaining');
    final skipped = _requiredIdList(json, 'skipped');
    _validatePartition(
      selection.ids,
      deleted: deleted,
      remaining: remaining,
      skipped: skipped,
    );

    if (_requiredNonNegativeInt(json, 'deleted_count') != deleted.length ||
        _requiredNonNegativeInt(json, 'remaining_count') != remaining.length ||
        _requiredNonNegativeInt(json, 'skipped_count') != skipped.length) {
      throw const FormatException(
        'Trash selection counts do not match their lists',
      );
    }

    final partial = _requiredBool(json, 'partial');
    if (partial != (remaining.isNotEmpty || skipped.isNotEmpty)) {
      throw const FormatException(
        'Trash selection partial state does not match its partition',
      );
    }

    return TrashEmptyResult._(
      deleted: deleted,
      remaining: remaining,
      skipped: skipped,
      partial: partial,
      cleanupWarning: _requiredBool(json, 'warning'),
    );
  }

  final List<String> deleted;
  final List<String> remaining;
  final List<String> skipped;
  final bool partial;
  final bool cleanupWarning;
}

String validateTrashItemId(Object? value) {
  if (value is! String ||
      value.isEmpty ||
      value.length > maxTrashItemIdLength ||
      !RegExp(r'^[A-Za-z0-9_-]+$').hasMatch(value)) {
    throw const FormatException('Invalid trash item ID');
  }
  return value;
}

void _validatePartition(
  List<String> requested, {
  required List<String> deleted,
  required List<String> remaining,
  required List<String> skipped,
}) {
  final positions = <String, int>{
    for (var index = 0; index < requested.length; index++)
      requested[index]: index,
  };
  final seen = <String>{};

  void validatePart(List<String> values) {
    var previousPosition = -1;
    for (final id in values) {
      final position = positions[id];
      if (position == null || position <= previousPosition || !seen.add(id)) {
        throw const FormatException(
          'Trash selection result is not an ordered partition',
        );
      }
      previousPosition = position;
    }
  }

  validatePart(deleted);
  validatePart(remaining);
  validatePart(skipped);
  if (seen.length != requested.length) {
    throw const FormatException(
      'Trash selection result does not cover every requested ID',
    );
  }
}

List<String> _requiredIdList(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value is! List) {
    throw FormatException('Invalid $key trash item list');
  }
  return value.map(validateTrashItemId).toList(growable: false);
}

bool _optionalWarning(Map<String, dynamic> json) {
  final value = json['warning'];
  if (value == null) {
    return false;
  }
  if (value is! bool) {
    throw const FormatException('Invalid mutation warning');
  }
  return value;
}

String _requiredCanonicalPath(Map<String, dynamic> json, String key) {
  final value = _requiredString(json, key);
  final normalized = normalizeLogicalPath(value, allowRoot: false);
  if (normalized != value) {
    throw FormatException('Invalid canonical $key');
  }
  return value;
}

DateTime _requiredRfc3339Timestamp(Map<String, dynamic> json, String key) {
  final value = _requiredString(json, key);
  final match = RegExp(
    r'^(\d{4})-(\d{2})-(\d{2})T'
    r'(\d{2}):(\d{2}):(\d{2})'
    r'(?:\.(\d{1,9}))?'
    r'(?:Z|([+-])(\d{2}):(\d{2}))$',
  ).firstMatch(value);
  if (match == null) {
    throw FormatException('Invalid $key timestamp');
  }

  final year = int.parse(match.group(1)!);
  final month = int.parse(match.group(2)!);
  final day = int.parse(match.group(3)!);
  final hour = int.parse(match.group(4)!);
  final minute = int.parse(match.group(5)!);
  final second = int.parse(match.group(6)!);
  final offsetHour = int.parse(match.group(9) ?? '0');
  final offsetMinute = int.parse(match.group(10) ?? '0');
  if (year == 0 ||
      month < 1 ||
      month > 12 ||
      day < 1 ||
      day > _daysInMonth(year, month) ||
      hour > 23 ||
      minute > 59 ||
      second > 59 ||
      offsetHour > 23 ||
      offsetMinute > 59) {
    throw FormatException('Invalid $key timestamp');
  }

  final parsed = DateTime.tryParse(value);
  if (parsed == null) {
    throw FormatException('Invalid $key timestamp');
  }
  return parsed.toUtc();
}

int _daysInMonth(int year, int month) {
  return switch (month) {
    2 when year % 4 == 0 && (year % 100 != 0 || year % 400 == 0) => 29,
    2 => 28,
    4 || 6 || 9 || 11 => 30,
    _ => 31,
  };
}

String _requiredString(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value is! String || value.isEmpty) {
    throw FormatException('Missing or invalid $key');
  }
  return value;
}

int _requiredNonNegativeInt(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value is! int || value < 0) {
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

Map<String, dynamic> _requireMap(Object? value) {
  if (value is Map<String, dynamic>) {
    return value;
  }
  if (value is Map) {
    return Map<String, dynamic>.from(value);
  }
  throw const FormatException('Expected a JSON object');
}
