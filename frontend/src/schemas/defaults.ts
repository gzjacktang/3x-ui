import { z } from 'zod';

export const DefaultsPayloadSchema = z.object({
  expireDiff: z.number().optional(),
  trafficDiff: z.number().optional(),
  pageSize: z.number().optional(),
  datepicker: z.enum(['gregorian', 'jalalian']).optional(),
  ipLimitEnable: z.boolean().optional(),
  accessLogEnable: z.boolean().optional(),
  webDomain: z.string().optional(),
}).loose();

export type DefaultsPayload = z.infer<typeof DefaultsPayloadSchema>;
