import { apiClient } from '@/lib/api';
import { ensureResponse, serviceCall } from '@/services/base/BaseService';
import type { Profile, ProfileInput, ProfileLogin } from '@/types/profile.types';

const NO_RETRY = { maxRetries: 0 };

async function listProfiles(): Promise<Profile[]> {
  return serviceCall(async () => {
    const response = await apiClient.get<Profile[]>('/profiles');
    return ensureResponse(response, 'Failed to load providers');
  });
}

async function createProfile(data: ProfileInput): Promise<Profile> {
  return serviceCall(async () => {
    const response = await apiClient.post<Profile>('/profiles', data);
    return ensureResponse(response, 'Failed to add account');
  });
}

async function updateProfile(profileId: string, data: ProfileInput): Promise<Profile> {
  return serviceCall(async () => {
    const response = await apiClient.patch<Profile>(`/profiles/${profileId}`, data);
    return ensureResponse(response, 'Failed to save account');
  });
}

async function deleteProfile(profileId: string): Promise<void> {
  await serviceCall(async () => {
    await apiClient.delete(`/profiles/${profileId}`);
  });
}

async function setDefaultProfile(profileId: string): Promise<Profile> {
  return serviceCall(async () => {
    const response = await apiClient.post<Profile>(`/profiles/${profileId}/default`);
    return ensureResponse(response, 'Failed to set the default account');
  });
}

// No retry: a second start would restart the CLI with a new sign-in link.
async function startLogin(profileId: string): Promise<ProfileLogin> {
  return serviceCall(async () => {
    const response = await apiClient.post<ProfileLogin>(`/profiles/${profileId}/login`);
    return ensureResponse(response, 'Failed to start sign-in');
  }, NO_RETRY);
}

// Polled, so the next poll is the retry.
async function getLogin(profileId: string): Promise<ProfileLogin> {
  return serviceCall(async () => {
    const response = await apiClient.get<ProfileLogin>(`/profiles/${profileId}/login`);
    return ensureResponse(response, 'Failed to read sign-in');
  }, NO_RETRY);
}

async function sendLoginInput(profileId: string, text: string): Promise<ProfileLogin> {
  return serviceCall(async () => {
    const response = await apiClient.post<ProfileLogin>(`/profiles/${profileId}/login/input`, {
      text,
    });
    return ensureResponse(response, 'Failed to send the code');
  });
}

async function cancelLogin(profileId: string): Promise<void> {
  await serviceCall(async () => {
    await apiClient.delete(`/profiles/${profileId}/login`);
  });
}

export const profileService = {
  listProfiles,
  createProfile,
  updateProfile,
  deleteProfile,
  setDefaultProfile,
  startLogin,
  getLogin,
  sendLoginInput,
  cancelLogin,
};
