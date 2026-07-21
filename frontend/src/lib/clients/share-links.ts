import { HttpUtil } from '@/utils';
import type { ClientRecord, InboundOption } from '@/schemas/client';
import { InboundDetailSchema } from '@/schemas/inbound';
import { inboundFromDb } from '@/lib/xray/inbound-from-db';
import { genAllLinks, getInboundClients, preferPublicHost } from '@/lib/xray/inbound-link';

export async function loadClientShareLinks(
  client: ClientRecord,
  inboundsById: Record<number, InboundOption>,
): Promise<string[]> {
  const batches = await Promise.all((client.inboundIds ?? []).map(async (id) => {
    const msg = await HttpUtil.get(`/panel/api/inbounds/get/${id}`, undefined, { silent: true });
    if (!msg?.success || !msg.obj) return [];
    const parsed = InboundDetailSchema.safeParse(msg.obj);
    if (!parsed.success) return [];

    const inbound = inboundFromDb(parsed.data as unknown as Parameters<typeof inboundFromDb>[0]);
    const attached = getInboundClients(inbound)?.find((entry) => entry.email === client.email);
    if (!attached) return [];

    const option = inboundsById[id];
    const fallbackHostname = preferPublicHost(window.location.hostname, '');
    return genAllLinks({
      inbound,
      remark: typeof parsed.data.remark === 'string' ? parsed.data.remark : '',
      client: attached,
      hostOverride: option?.nodeAddress || '',
      fallbackHostname,
    }).map((entry) => entry.link).filter(Boolean);
  }));

  return [...new Set(batches.flat())];
}
