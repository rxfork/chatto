import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ parent }) => {
  const { serverInfo } = await parent();
  if (serverInfo?.emailDisabled) redirect(302, '/login');
  return {};
};
