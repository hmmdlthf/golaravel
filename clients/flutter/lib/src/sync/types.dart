// Wire types, mirroring pkg/offline's JSON (and @golaravel/offline's types.ts).

/// What a mutation does to its entity.
enum Op {
  create,
  update,
  delete;

  static Op parse(String s) => Op.values.firstWhere((o) => o.name == s, orElse: () => Op.update);
}

/// One immutable outbox entry.
class Mutation {
  Mutation({
    required this.id,
    required this.entity,
    required this.entityId,
    required this.op,
    required this.hlc,
    required this.at,
    this.set,
    this.inc,
    this.group,
    this.baseVersion,
  });

  final String id;
  final String entity;
  final String entityId;
  final Op op;
  final Map<String, Object?>? set;
  final Map<String, num>? inc;
  final String hlc;
  final String? group;
  final int? baseVersion;

  /// Client wall time (Unix ms).
  final int at;

  Map<String, Object?> toJson() => {
        'id': id,
        'entity': entity,
        'entity_id': entityId,
        'op': op.name,
        if (set != null) 'set': set,
        if (inc != null) 'inc': inc,
        'hlc': hlc,
        if (group != null) 'group': group,
        if (baseVersion != null) 'base_version': baseVersion,
        'at': at,
      };

  factory Mutation.fromJson(Map<String, Object?> j) => Mutation(
        id: j['id'] as String,
        entity: j['entity'] as String,
        entityId: j['entity_id'] as String,
        op: Op.parse(j['op'] as String),
        set: (j['set'] as Map?)?.cast<String, Object?>(),
        inc: (j['inc'] as Map?)?.cast<String, num>(),
        hlc: j['hlc'] as String,
        group: j['group'] as String?,
        baseVersion: (j['base_version'] as num?)?.toInt(),
        at: (j['at'] as num).toInt(),
      );
}

/// The server's answer for one mutation.
enum Status {
  applied,
  resolved,
  duplicate,
  rejected,
  retry;

  static Status? parse(String? s) => s == null ? null : Status.values.where((v) => v.name == s).firstOrNull;
}

class ServerRecord {
  ServerRecord({required this.id, required this.version, this.hlc, this.deleted = false, this.data});

  final String id;
  final int version;
  final String? hlc;
  final bool deleted;
  final Map<String, Object?>? data;

  factory ServerRecord.fromJson(Map<String, Object?> j) => ServerRecord(
        id: j['id'] as String,
        version: (j['version'] as num?)?.toInt() ?? 0,
        hlc: j['hlc'] as String?,
        deleted: j['deleted'] == true,
        data: (j['data'] as Map?)?.cast<String, Object?>(),
      );
}

class PushResult {
  PushResult({required this.id, required this.status, this.original, this.seq, this.reason, this.record});

  final String id;
  final Status status;
  final Status? original;
  final int? seq;
  final String? reason;
  final ServerRecord? record;

  factory PushResult.fromJson(Map<String, Object?> j) => PushResult(
        id: j['id'] as String,
        status: Status.parse(j['status'] as String?) ?? Status.retry,
        original: Status.parse(j['original'] as String?),
        seq: (j['seq'] as num?)?.toInt(),
        reason: j['reason'] as String?,
        record: j['record'] is Map ? ServerRecord.fromJson((j['record'] as Map).cast()) : null,
      );
}

/// One server change from a pull.
class RemoteChange {
  RemoteChange({required this.entity, required this.entityId, this.deleted = false, this.version, this.hlc, this.data});

  final String entity;
  final String entityId;
  final bool deleted;
  final int? version;
  final String? hlc;
  final Map<String, Object?>? data;

  factory RemoteChange.fromJson(Map<String, Object?> j) => RemoteChange(
        entity: j['entity'] as String,
        entityId: j['entity_id'] as String,
        deleted: j['deleted'] == true,
        version: (j['version'] as num?)?.toInt(),
        hlc: j['hlc'] as String?,
        data: (j['data'] as Map?)?.cast<String, Object?>(),
      );
}

/// A device registered with the server.
class Device {
  Device({required this.id, required this.tenantId, this.serverKid, this.serverPublicKey, required this.json});

  final String id;
  final String tenantId;
  final String? serverKid;
  final String? serverPublicKey;

  /// The server's full answer, as stored.
  final Map<String, Object?> json;

  factory Device.fromJson(Map<String, Object?> j) {
    final key = j['server_key'] is Map ? (j['server_key'] as Map).cast<String, Object?>() : null;
    return Device(
      id: j['id'] as String,
      tenantId: j['tenant_id'] as String? ?? '',
      serverKid: key?['kid'] as String?,
      serverPublicKey: key?['public_key'] as String?,
      json: j,
    );
  }
}

/// A mutation the server refused for good.
class DeadLetter {
  DeadLetter({required this.id, required this.mutation, required this.reason, required this.at});

  final String id;
  final Mutation mutation;
  final String reason;
  final int at;

  Map<String, Object?> toJson() => {'id': id, 'mutation': mutation.toJson(), 'reason': reason, 'at': at};

  factory DeadLetter.fromJson(Map<String, Object?> j) => DeadLetter(
        id: j['id'] as String,
        mutation: Mutation.fromJson((j['mutation'] as Map).cast()),
        reason: j['reason'] as String? ?? 'rejected',
        at: (j['at'] as num).toInt(),
      );
}

/// An entity whose local state changed.
class Touched {
  const Touched(this.entity, this.id);

  final String entity;
  final String id;

  @override
  bool operator ==(Object other) => other is Touched && other.entity == entity && other.id == id;

  @override
  int get hashCode => Object.hash(entity, id);

  @override
  String toString() => '$entity/$id';
}
